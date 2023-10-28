package convertmachinetype

import (
	"context"
	"fmt"
	"path"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	v1 "kubevirt.io/api/core/v1"

	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"
)

// using these as consts allows us to easily modify the program to update as newer versions are released
// we generally want to be updating the machine types to the most recent version

var (
	// machine type(s) which should be updated
	MachineTypeGlob = ""
	LabelSelector   labels.Selector
	// by default, update machine type across all namespaces
	Namespace = metav1.NamespaceAll
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

func (c *JobController) patchMachineType(vm *v1.VirtualMachine) error {
	// removing the machine type field from the VM spec reverts it to
	// the default machine type of the VM's arch
	updateMachineType := `[{"op": "remove", "path": "/spec/template/spec/domain/machine"}]`

	_, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(updateMachineType), &metav1.PatchOptions{})
	return err
}

func isMachineTypeUpdated(obj interface{}) (bool, error) {
	vm, ok := obj.(*v1.VirtualMachine)

	if ok {
		machine := vm.Spec.Template.Spec.Domain.Machine
		matchesGlob := false
		var err error

		if machine != nil {
			matchesGlob, err = matchMachineType(machine.Type)
			if err != nil {
				return false, err
			}
		}
		return machine.Type == virtconfig.DefaultAMD64MachineType || !matchesGlob, nil
	}

	vmi := obj.(*v1.VirtualMachineInstance)
	specMachine := vmi.Spec.Domain.Machine
	statusMachine := vmi.Status.Machine
	if specMachine == nil || statusMachine == nil {
		return false, fmt.Errorf("vmi machine type is not set properly")
	}
	matchesGlob, err := matchMachineType(statusMachine.Type)
	if err != nil {
		return false, err
	}
	return specMachine.Type == virtconfig.DefaultAMD64MachineType || !matchesGlob, nil
}

func (c *JobController) UpdateMachineType(vm *v1.VirtualMachine, running bool) error {
	err := c.patchMachineType(vm)
	if err != nil {
		return err
	}

	if running {
		// if force restart flag is set, restart running VMs immediately
		// don't apply warning label to VMs being restarted
		if RestartNow {
			err = c.VirtClient.VirtualMachine(vm.Namespace).Restart(context.Background(), vm.Name, &v1.RestartOptions{})
			if err != nil {
				return err
			}
		}

		// adding the warning label to the running VMs to indicate to the user
		// they must manually be restarted
		err = c.addWarningLabel(vm)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *JobController) addWarningLabel(vm *v1.VirtualMachine) error {
	addLabel := `{"metadata":{"labels":{"restart-vm-required":""}}}`

	vm, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.MergePatchType, []byte(addLabel), &metav1.PatchOptions{})
	return err
}
