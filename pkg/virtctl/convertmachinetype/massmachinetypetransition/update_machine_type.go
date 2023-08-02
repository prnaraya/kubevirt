package massmachinetypetransition

import (
	"context"
	"fmt"
	"strings"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

// using these as consts allows us to easily modify the program to update as newer versions are released
// we generally want to be updating the machine types to the most recent version
const (
	LatestMachineTypeVersion           = "rhel9.2.0"
	MinimumSupportedMachineTypeVersion = "rhel9.0.0"
)

var (
	VmisPendingUpdate = make(map[string]struct{})
	exitJob           = make(chan struct{})

	// by default, update machine type across all namespaces
	namespace     = k8sv1.NamespaceAll
	labelSelector = ""

	// by default, should require manual restarting of VMIs
	restartNow = false
)

func VerifyMachineType(machineType string) (bool, string) {
	// if the machine type is "q35", we want the VM to behave as though its machine type has been updated
	if machineType == "q35" {
		return true, machineType
	}

	machineTypeSubstrings := strings.Split(machineType, "-")

	if len(machineTypeSubstrings) == 3 {
		version := machineTypeSubstrings[2]
		if strings.Contains(version, "rhel") && version < MinimumSupportedMachineTypeVersion {
			machineTypeSubstrings[2] = LatestMachineTypeVersion
			machineType = strings.Join(machineTypeSubstrings, "-")
			return true, machineType
		}
	}
	return false, machineType
}

func UpdateMachineTypes(virtCli kubecli.KubevirtClient) error {
	listOpts := &k8sv1.ListOptions{}
	if labelSelector != "" {
		listOpts = &k8sv1.ListOptions{
			LabelSelector: labelSelector,
		}
	}
	vmList, err := virtCli.VirtualMachine(namespace).List(context.Background(), listOpts)

	if err != nil {
		return err
	}

	for _, vm := range vmList.Items {
		machineType := vm.Spec.Template.Spec.Domain.Machine.Type
		needsUpdate, machineType := VerifyMachineType(machineType)

		// skip VM if its machine type is supported
		if !needsUpdate {
			continue
		}

		if machineType != "q35" {
			updateMachineType := fmt.Sprintf(`{"spec":{"template":{"spec":{"domain":{"machine":{"type":"%s"}}}}}}`, machineType)

			_, err := virtCli.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.MergePatchType, []byte(updateMachineType), &k8sv1.PatchOptions{})
			if err != nil {
				fmt.Print(err)
				continue
			}
		}

		// add label to running VMs that a restart is required for change to take place
		if *vm.Spec.Running {
			vmi, err := virtCli.VirtualMachineInstance(vm.Namespace).Get(context.Background(), vm.Name, &k8sv1.GetOptions{})
			if err != nil {
				fmt.Print(err)
				continue
			}

			// only need to restart VM if VMI machine type is outdated (in the case of "q35" machine types
			needsUpdate, _ := VerifyMachineType(vmi.Status.Machine.Type)
			if !needsUpdate {
				continue
			}

			// adding the warning label to the VMs regardless if we restart them now or if the user does it manually
			// shouldn't matter, since the deletion of the VMI will remove the label and remove the vmi list anyway
			err = AddWarningLabel(virtCli, &vm)
			if err != nil {
				fmt.Print(err)
				continue
			}
		}

		if restartNow {
			err = virtCli.VirtualMachine(vm.Namespace).Restart(context.Background(), vm.Name, &k6tv1.RestartOptions{})
			if err != nil {
				fmt.Print(err)
				continue
			}
		}
	}
	return nil
}

func AddWarningLabel(virtCli kubecli.KubevirtClient, vm *k6tv1.VirtualMachine) error {
	addLabel := `{"metadata":{"labels":{"restart-vm-required":"true"}}}`

	_, err := virtCli.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.MergePatchType, []byte(addLabel), &k8sv1.PatchOptions{})
	if err != nil {
		return err
	}

	// get VM name in the format namespace/name
	vmKey, err := cache.MetaNamespaceKeyFunc(vm)
	if err != nil {
		return err
	}
	VmisPendingUpdate[vmKey] = struct{}{}

	return nil
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
