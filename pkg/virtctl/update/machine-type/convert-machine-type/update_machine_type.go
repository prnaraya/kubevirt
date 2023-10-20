package convertmachinetype

import (
	"context"
	"fmt"
	"path"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k6tv1 "kubevirt.io/api/core/v1"
)

// using these as consts allows us to easily modify the program to update as newer versions are released
// we generally want to be updating the machine types to the most recent version

var (
	// machine type(s) which should be updated
	MachineTypeGlob = ""

	// by default, update machine type across all namespaces
	Namespace     = k8sv1.NamespaceAll
	LabelSelector = ""

	// by default, should require manual restarting of VMIs
	RestartNow = false
)

func matchMachineType(machineType string) (bool, error) {
	matchMachineType, err := path.Match(MachineTypeGlob, machineType)
	if !matchMachineType || err != nil {
		return false, err
	}

	return true, nil
}

func (c *JobController) patchMachineType(vm *k6tv1.VirtualMachine) error {
	// removing the machine type field from the VM spec reverts it to
	// the default machine type of the VM's arch
	updateMachineType := `[{"op": "remove", "path": "/spec/template/spec/domain/machine"}]`

	_, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(updateMachineType), &k8sv1.PatchOptions{})
	return err
}

func isMachineTypeUpdated(vm *k6tv1.VirtualMachine) bool {
	return vm.Spec.Template.Spec.Domain.Machine == nil
}

func (c *JobController) UpdateMachineTypes() error {
	listOpts := &k8sv1.ListOptions{}
	if LabelSelector != "" {
		listOpts = &k8sv1.ListOptions{
			LabelSelector: LabelSelector,
		}
	}
	vmList, err := c.VirtClient.VirtualMachine(Namespace).List(context.Background(), listOpts)

	if err != nil {
		return err
	}

	for _, vm := range vmList.Items {

		if (vm.Spec.Running != nil && *vm.Spec.Running) || (vm.Spec.RunStrategy != nil && *vm.Spec.RunStrategy != k6tv1.RunStrategyHalted) {
			vmi, err := c.VirtClient.VirtualMachineInstance(vm.Namespace).Get(context.Background(), vm.Name, &k8sv1.GetOptions{})
			if err != nil {
				fmt.Print(err)
				continue
			}

			// check that VMI is in Running phase
			if vmi.Status.Phase != k6tv1.Running {
				continue
			}

			// for running VMs, check if machine type in VMI status matches glob
			matchMachineType, err := path.Match(MachineTypeGlob, vmi.Status.Machine.Type)
			if !matchMachineType {
				if err != nil {
					fmt.Print(err)
				}
				continue
			}

			c.patchMachineType(&vm)

			// if force restart flag is set, restart running VMs immediately
			// don't apply warning label to VMs being restarted
			if RestartNow {
				err = c.VirtClient.VirtualMachine(vm.Namespace).Restart(context.Background(), vm.Name, &k6tv1.RestartOptions{})
				if err != nil {
					fmt.Print(err)
				}
				continue
			}

			// adding the warning label to the running VMs to indicate to the user
			// they must manually be restarted
			err = c.addWarningLabel(&vm)
			if err != nil {
				fmt.Print(err)
				continue
			}
		} else {
			// for stopped VMs, check if machine type in VM spec matches glob
			matchMachineType, err := matchMachineType(vm.Spec.Template.Spec.Domain.Machine.Type)

			if !matchMachineType {
				if err != nil {
					fmt.Print(err)
				}
				continue
			}

			c.patchMachineType(&vm)
		}
	}
	return nil
}

func (c *JobController) addWarningLabel(vm *k6tv1.VirtualMachine) error {
	addLabel := `{"metadata":{"labels":{"restart-vm-required":""}}}`

	vm, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.MergePatchType, []byte(addLabel), &k8sv1.PatchOptions{})
	return err
}
