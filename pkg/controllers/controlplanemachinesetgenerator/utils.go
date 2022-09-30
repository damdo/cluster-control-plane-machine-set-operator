/*
Copyright 2022 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controlplanemachinesetgenerator

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ghodss/yaml"
	"github.com/go-test/deep"
	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	machinev1builder "github.com/openshift/client-go/machine/applyconfigurations/machine/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// rawExtensionFromProviderSpec decodes a generic Provider Spec Raw value into a flavored Provider Spec.
func providerSpecFromRawExtension(platformType configv1.PlatformType, rawExtension *runtime.RawExtension) (interface{}, error) {
	if rawExtension == nil {
		return nil, errEmptyProviderSpec
	}

	switch platformType {
	case configv1.AWSPlatformType:
		var spec machinev1beta1.AWSMachineProviderConfig
		if err := yaml.Unmarshal(rawExtension.Raw, &spec); err != nil {
			return nil, fmt.Errorf("error unmarshalling providerSpec: %w", err)
		}

		return spec, nil
	default:
		return nil, errUnsupportedPlatform
	}
}

// rawExtensionFromProviderSpec encodes a flavored Provider Spec into a generic Provider Spec Raw value.
func rawExtensionFromProviderSpec(platformType configv1.PlatformType, spec interface{}) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	var (
		rawBytes []byte
		err      error
	)

	switch platformType {
	case configv1.AWSPlatformType:
		spec, ok := spec.(machinev1beta1.AWSMachineProviderConfig)
		if !ok {
			return nil, errFailedProviderSpecTypeAssertion
		}

		if rawBytes, err = json.Marshal(spec); err != nil {
			return nil, fmt.Errorf("error marshalling providerSpec: %w", err)
		}

		return &runtime.RawExtension{
			Raw: rawBytes,
		}, nil
	default:
		return nil, errUnsupportedPlatform
	}
}

// sortMachinesByCreationTimeDescending sorts a slice of Machines by CreationTime, Name (descending).
func sortMachinesByCreationTimeDescending(machines []machinev1beta1.Machine) []machinev1beta1.Machine {
	// Sort in inverse order so that the newest one is first.
	sort.Slice(machines, func(i, j int) bool {
		first, second := machines[i].CreationTimestamp, machines[j].CreationTimestamp
		if first != second {
			return second.Before(&first)
		}

		return machines[i].Name > machines[j].Name
	})

	return machines
}

// sortMachineSetsByCreationTimeAscending sorts a slice of MachineSets by CreationTime, Name (ascending).
func sortMachineSetsByCreationTimeAscending(machineSets []machinev1beta1.MachineSet) []machinev1beta1.MachineSet {
	sort.Slice(machineSets, func(i, j int) bool {
		first, second := machineSets[i].CreationTimestamp, machineSets[j].CreationTimestamp
		if first != second {
			return !second.Before(&first)
		}

		return machineSets[i].Name < machineSets[j].Name
	})

	return machineSets
}

// controlPlaneMachineSetApplyConfigToControlPlaneMachineSet converts a ControlPlaneMachineSetApplyConfig to a ControlPlaneMachineSet.
func controlPlaneMachineSetApplyConfigToControlPlaneMachineSet(cpmsApplyConfig machinev1builder.ControlPlaneMachineSetApplyConfiguration) (machinev1.ControlPlaneMachineSet, error) {
	b, err := json.Marshal(cpmsApplyConfig)
	if err != nil {
		return machinev1.ControlPlaneMachineSet{}, fmt.Errorf("failed to encode control plane machine set: %w", err)
	}

	cpms := machinev1.ControlPlaneMachineSet{}
	if err := json.Unmarshal(b, &cpms); err != nil {
		return machinev1.ControlPlaneMachineSet{}, fmt.Errorf("failed to decode control plane machine set: %w", err)
	}

	return cpms, nil
}

// genericControlPlaneMachineSetSpec returns a generic ControlPlaneMachineSet spec, without provider specific details.
func genericControlPlaneMachineSetSpec(replicas int32, clusterID string) machinev1builder.ControlPlaneMachineSetSpecApplyConfiguration {
	labels := map[string]string{
		clusterIDLabelKey:          clusterID,
		clusterMachineRoleLabelKey: clusterMachineLabelValueMaster,
		clusterMachineTypeLabelKey: clusterMachineLabelValueMaster,
	}
	machineType := machinev1.OpenShiftMachineV1Beta1MachineType
	strategy := machinev1.RollingUpdate
	state := machinev1.ControlPlaneMachineSetStateInactive

	return machinev1builder.ControlPlaneMachineSetSpecApplyConfiguration{
		Replicas: &replicas,
		State:    &state,
		Strategy: &machinev1builder.ControlPlaneMachineSetStrategyApplyConfiguration{
			Type: &strategy,
		},
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: &machinev1builder.ControlPlaneMachineSetTemplateApplyConfiguration{
			MachineType: &machineType,
			OpenShiftMachineV1Beta1Machine: &machinev1builder.OpenShiftMachineV1Beta1MachineTemplateApplyConfiguration{
				ObjectMeta: &machinev1builder.ControlPlaneMachineSetTemplateObjectMetaApplyConfiguration{
					Labels: labels,
				},
			},
		},
	}
}

// compareControlPlaneMachineSets does a comparison of two ControlPlaneMachineSets also based on their PlatformType and returns any difference found.
func compareControlPlaneMachineSets(platformType configv1.PlatformType, a, b *machinev1.ControlPlaneMachineSet) ([]string, error) {
	// We need to compare the providerSpecs and the rest of the ControlPlaneMachineSets specs separately,
	// as the formers are marshalled and need to be unmarshaled to be compared.
	aProviderSpec, err := providerSpecFromRawExtension(platformType, a.Spec.Template.OpenShiftMachineV1Beta1Machine.Spec.ProviderSpec.Value)
	if err != nil {
		return []string{}, fmt.Errorf("failed to decode providerSpec raw value: %w", err)
	}

	bProviderSpec, err := providerSpecFromRawExtension(platformType, b.Spec.Template.OpenShiftMachineV1Beta1Machine.Spec.ProviderSpec.Value)
	if err != nil {
		return []string{}, fmt.Errorf("failed to decode providerSpec raw value: %w", err)
	}

	providerSpecDiff := deep.Equal(aProviderSpec, bProviderSpec)

	// Remove the providerSpec from the ControlPlaneMachineSet Specs as we've already compared them.
	aCopy := a.DeepCopy()
	aCopy.Spec.Template.OpenShiftMachineV1Beta1Machine.Spec.ProviderSpec.Value = nil

	bCopy := b.DeepCopy()
	bCopy.Spec.Template.OpenShiftMachineV1Beta1Machine.Spec.ProviderSpec.Value = nil

	cpmsSpecDiff := deep.Equal(aCopy.Spec, bCopy.Spec)

	// Combine the two diffs found.
	var diff []string
	diff = append(diff, cpmsSpecDiff...)
	diff = append(diff, providerSpecDiff...)

	return diff, nil
}

// mergeMachineSlices merges two machine slices into one, removing duplicates.
func mergeMachineSlices(a []machinev1beta1.Machine, b []machinev1beta1.Machine) []machinev1beta1.Machine {
	combined := []machinev1beta1.Machine{}
	combined = append(combined, a...)
	combined = append(combined, b...)

	allKeys := make(map[string]struct{})
	list := []machinev1beta1.Machine{}

	for _, item := range combined {
		if _, value := allKeys[item.Name]; !value {
			allKeys[item.Name] = struct{}{}

			list = append(list, item)
		}
	}

	return list
}
