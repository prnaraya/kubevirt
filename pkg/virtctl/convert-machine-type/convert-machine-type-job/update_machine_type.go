package convertmachinetypejob

import (
	"context"
	"fmt"
	"strings"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k6tv1 "kubevirt.io/api/core/v1"
)

// using these as consts allows us to easily modify the program to update as newer versions are released
// we generally want to be updating the machine types to the most recent version
const (
	LatestMachineTypeVersion           = "rhel9.2.0"
	MinimumSupportedMachineTypeVersion = "rhel9.0.0"
)

var (
	// by default, update machine type across all namespaces
	namespace     = k8sv1.NamespaceAll
	labelSelector = ""

	// by default, should require manual restarting of VMIs
	restartNow = false
)

func IsMachineTypeUpdated(machineType string) (isUpdated bool, updatedMachineType string, err error) {

	// if the machine type is "q35", we want the VM to behave as though its machine type has been updated
	if machineType == "q35" {
		return true, machineType, nil
	}

	machineTypeSubstrings := strings.Split(machineType, "-")

	if len(machineTypeSubstrings) != 3 {
		return false, machineType, fmt.Errorf("invalid machine type: %s", machineType)
	}

	if len(machineTypeSubstrings) == 3 {
		version := machineTypeSubstrings[2]
		if strings.Contains(version, "rhel") && version < MinimumSupportedMachineTypeVersion {
			machineTypeSubstrings[2] = LatestMachineTypeVersion
			machineType = strings.Join(machineTypeSubstrings, "-")
			return true, machineType, nil
		}
	}
	return false, machineType, nil
}

func (c *JobController) UpdateMachineTypes() error {
	listOpts := &k8sv1.ListOptions{}
	if labelSelector != "" {
		listOpts = &k8sv1.ListOptions{
			LabelSelector: labelSelector,
		}
	}
	vmList, err := c.VirtClient.VirtualMachine(namespace).List(context.Background(), listOpts)

	if err != nil {
		return err
	}

	for _, vm := range vmList.Items {
		machineType := vm.Spec.Template.Spec.Domain.Machine.Type
		needsUpdate, machineType, err := IsMachineTypeUpdated(machineType)

		if err != nil {
			fmt.Print(err)
			continue
		}

		// skip VM if its machine type is supported
		if !needsUpdate {
			continue
		}

		if machineType != "q35" {
			updateMachineType := fmt.Sprintf(`{"spec":{"template":{"spec":{"domain":{"machine":{"type":"%s"}}}}}}`, machineType)

			_, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.MergePatchType, []byte(updateMachineType), &k8sv1.PatchOptions{})
			if err != nil {
				fmt.Print(err)
				continue
			}
		}

		// add label to running VMs that a restart is required for change to take place
		if *vm.Spec.Running {
			vmi, err := c.VirtClient.VirtualMachineInstance(vm.Namespace).Get(context.Background(), vm.Name, &k8sv1.GetOptions{})
			if err != nil {
				fmt.Print(err)
				continue
			}

			// verify that VMI is in running phase
			// if vmi.Status.Phase != k6tv1.Running {

			// }

			// only need to restart VM if VMI machine type is outdated (in the case of "q35" machine types
			needsUpdate, _, err := IsMachineTypeUpdated(vmi.Status.Machine.Type)

			// needsUpdate will always be false when the function returns and error
			if !needsUpdate {
				if err != nil {
					fmt.Print(err)
				}
				continue
			}

			// if force restart flag is set, restart running VMs immediately
			if restartNow {
				err = c.VirtClient.VirtualMachine(vm.Namespace).Restart(context.Background(), vm.Name, &k6tv1.RestartOptions{})
				if err != nil {
					fmt.Print(err)
					continue
				}
			}

			// adding the warning label to the VMs to indicate to the user
			// they must manually be restarted
			err = c.AddWarningLabel(&vm)
			if err != nil {
				fmt.Print(err)
				continue
			}
		}
	}
	return nil
}

func (c *JobController) AddWarningLabel(vm *k6tv1.VirtualMachine) error {
	addLabel := `{"metadata":{"labels":{"restart-vm-required":""}}}`

	vm, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.MergePatchType, []byte(addLabel), &k8sv1.PatchOptions{})
	return err
}

func SetTestRestartNow(testRestart bool) {
	restartNow = testRestart
}

func SetTestNamespace(testNamespace string) {
	namespace = testNamespace
}

func SetTestLabelSelector(testLabelSelector string) {
	labelSelector = testLabelSelector
}
