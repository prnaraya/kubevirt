package massmachinetypetransition

import (
	"context"
	"fmt"
	"time"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

func getVirtCli() (kubecli.KubevirtClient, error) {
	clientConfig, err := kubecli.GetKubevirtClientConfig()
	if err != nil {
		return nil, err
	}

	virtCli, err := kubecli.GetKubevirtClientFromRESTConfig(clientConfig)
	if err != nil {
		return nil, err
	}

	return virtCli, err
}

func getVmiInformer(virtCli kubecli.KubevirtClient) (cache.SharedIndexInformer, error) {
	listWatcher := cache.NewListWatchFromClient(virtCli.RestClient(), "virtualmachineinstances", namespace, fields.Everything())
	vmiInformer := cache.NewSharedIndexInformer(listWatcher, &k6tv1.VirtualMachineInstance{}, 1*time.Hour, cache.Indexers{})

	vmiInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: handleDeletedVmi,
	})

	return vmiInformer, nil
}

func handleDeletedVmi(obj interface{}) {
	vmi, ok := obj.(*k6tv1.VirtualMachineInstance)
	if !ok {
		return
	}

	virtCli, err := getVirtCli()
	if err != nil {
		fmt.Println(err)
		return
	}

	vm, err := virtCli.VirtualMachine(vmi.Namespace).Get(context.Background(), vmi.Name, &k8sv1.GetOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}

	// check if VM has restart-vm-required label
	if _, ok := vm.Labels["restart-vm-required"]; !ok {
		return
	}

	// check that VM machine type is not outdated in the case that a VM has been restarted for
	// a different reason before updating its machine type
	pendingUpdate, _ := VerifyMachineType(vm.Spec.Template.Spec.Domain.Machine.Type)
	if pendingUpdate {
		return
	}

	// get VMI name in the format namespace/name
	vmiKey, err := cache.MetaNamespaceKeyFunc(vmi)
	if err != nil {
		fmt.Println(err)
		return
	}

	// check if deleted VMI is in list of VMIs that need to be restarted
	_, exists := VmisPendingUpdate[vmiKey]
	if !exists {
		return
	}

	// remove warning label from VM
	// if removing the warning label fails, exit before removing VMI from list
	// since the label is still there to tell the user to restart, it wouldn't
	// make sense to have a mismatch between the number of VMs with the label
	// and the number of VMIs in the list of VMIs pending update.

	err = RemoveWarningLabel(virtCli, vm)
	if err != nil {
		fmt.Println(err)
	}

	// remove deleted VMI from list
	delete(VmisPendingUpdate, vmiKey)

	// check if VMI list is now empty, to signal exiting the job
	if len(VmisPendingUpdate) == 0 {
		// as a sanity check, ensure all 'restart-vm-required' labels
		// on affected VMs are cleared using label selector
		/*vmList, err := virtCli.VirtualMachine(namespace).List(context.Background(), &k8sv1.ListOptions{
			LabelSelector: "restart-vm-required=true",
		})*/
		close(exitJob)
	}
}

func RemoveWarningLabel(virtCli kubecli.KubevirtClient, vm *k6tv1.VirtualMachine) error {
	removeLabel := fmt.Sprint(`{"op": "remove", "path": "/metadata/labels/restart-vm-required"}`)
	_, err := virtCli.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(removeLabel), &k8sv1.PatchOptions{})
	return err
}
