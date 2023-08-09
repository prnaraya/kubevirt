package massmachinetypetransition

import (
	"context"
	"fmt"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

type JobController struct {
	VmiInformer cache.SharedIndexInformer
	VirtClient  kubecli.KubevirtClient
	ExitJob     chan struct{}
}

func NewJobController(
	vmiInformer cache.SharedIndexInformer,
	virtClient kubecli.KubevirtClient,
	exitJob chan struct{},
) (*JobController, error) {
	c := &JobController{
		VmiInformer: vmiInformer,
		VirtClient:  virtClient,
		ExitJob:     exitJob,
	}

	_, err := vmiInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: c.HandleDeletedVmi,
	})
	if err != nil {
		return nil, err
	}

	return c, nil
}

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

func (c *JobController) HandleDeletedVmi(obj interface{}) {
	vmi, ok := obj.(*k6tv1.VirtualMachineInstance)
	if !ok {
		return
	}

	vm, err := c.VirtClient.VirtualMachine(vmi.Namespace).Get(context.Background(), vmi.Name, &k8sv1.GetOptions{})
	if err != nil {
		fmt.Println(err)
		return
	}

	// check if VM has restart-vm-required label
	if _, ok := vm.Labels["restart-vm-required"]; !ok {
		return
	}

	// check that VM machine type is not outdated in the case that a VM
	// has been restarted for a different reason before its machine type
	// has been updated
	pendingUpdate, _ := IsMachineTypeUpdated(vmi.Status.Machine.Type)
	if pendingUpdate {
		return
	}

	// remove warning label from VM
	err = c.RemoveWarningLabel(vm)
	if err != nil {
		fmt.Println(err)
	}

	// ensure all 'restart-vm-required' labels on affected VMs
	// are cleared using label selector
	numVmisPendingUpdate := c.numVmisPendingUpdate()
	if numVmisPendingUpdate != 0 {
		return
	}

	close(c.ExitJob)
}

func (c *JobController) RemoveWarningLabel(vm *k6tv1.VirtualMachine) error {
	removeLabel := `[{"op": "remove", "path": "/metadata/labels/restart-vm-required"}]`
	_, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(removeLabel), &k8sv1.PatchOptions{})
	return err
}

func (c *JobController) numVmisPendingUpdate() int {
	vmList, err := c.VirtClient.VirtualMachine(namespace).List(context.Background(), &k8sv1.ListOptions{
		LabelSelector: "restart-vm-required=true",
	})
	if err != nil {
		fmt.Println(err)
		return -1
	}

	return len(vmList.Items)
}
