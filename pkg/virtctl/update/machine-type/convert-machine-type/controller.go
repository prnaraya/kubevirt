package convertmachinetype

import (
	"fmt"
	"time"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/pkg/util/status"
)

type JobController struct {
	VmInformer    cache.SharedIndexInformer
	VmiInformer   cache.SharedIndexInformer
	VirtClient    kubecli.KubevirtClient
	Queue         workqueue.RateLimitingInterface
	statusUpdater *status.VMStatusUpdater
	ExitJob       chan struct{}
}

func NewJobController(
	vmInformer, vmiInformer cache.SharedIndexInformer,
	virtClient kubecli.KubevirtClient,
) (*JobController, error) {
	c := &JobController{
		VmInformer:    vmInformer,
		VmiInformer:   vmiInformer,
		VirtClient:    virtClient,
		Queue:         workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		statusUpdater: status.NewVMStatusUpdater(virtClient),
		ExitJob:       make(chan struct{}),
	}

	_, err := vmInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.vmHandler,
		UpdateFunc: func(_, newObj interface{}) { c.vmHandler(newObj) },
	})
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *JobController) exitJob() {
	vms := c.VmInformer.GetStore().List()
	outdatedVms := 0
	vmsPendingRestart := 0

	for _, obj := range vms {
		vm := obj.(*v1.VirtualMachine)
		updated, err := isMachineTypeUpdated(vm)
		if err != nil {
			fmt.Println(err)
			return
		}
		if !updated {
			outdatedVms++
		} else if vm.Status.MachineTypeRestartRequired {
			vmsPendingRestart++
		}
	}

	if outdatedVms == 0 && vmsPendingRestart == 0 {
		close(c.ExitJob)
	}
}

func (c *JobController) run(stopCh <-chan struct{}) {
	defer c.Queue.ShutDown()
	informerStopCh := make(chan struct{})

	fmt.Println("Starting job controller")
	go c.VmInformer.Run(informerStopCh)
	go c.VmiInformer.Run(informerStopCh)

	if !cache.WaitForCacheSync(informerStopCh, c.VmInformer.HasSynced, c.VmiInformer.HasSynced) {
		fmt.Println("Timed out waiting for caches to sync")
		return
	}

	vmKeys := c.VmInformer.GetStore().ListKeys()
	fmt.Printf("Num vm keys: %d\n", len(vmKeys))
	for _, k := range vmKeys {
		c.Queue.Add(k)
	}

	wait.Until(c.runWorker, time.Second, stopCh)
}

func (c *JobController) runWorker() {
	for c.Execute() {
		c.exitJob()
	}
}

func (c *JobController) Execute() bool {
	key, quit := c.Queue.Get()
	if quit {
		close(c.ExitJob)
		return false
	}

	defer c.Queue.Done(key)

	if err := c.execute(key.(string)); err != nil {
		c.Queue.AddRateLimited(key)
	} else {
		c.Queue.Forget(key)
	}

	return true
}

func (c *JobController) vmHandler(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err == nil {
		c.Queue.Add(key)
	}
}

func (c *JobController) execute(key string) error {
	obj, exists, err := c.VmInformer.GetStore().GetByKey(key)
	if err != nil || !exists {
		return nil
	}

	vm := obj.(*v1.VirtualMachine)

	fmt.Printf("VM: %s, labels: %v, machine type: %s, running: %t\n", vm.Name, vm.Labels, vm.Spec.Template.Spec.Domain.Machine.Type, *vm.Spec.Running)
	// check if VM is running
	isRunning, err := vmIsRunning(vm)
	if err != nil {
		fmt.Println(err)
		return err
	}

	// check if VM machine type was updated
	updated, err := isMachineTypeUpdated(vm)
	if err != nil {
		fmt.Println(err)
		return err
	}

	// update VMs that require update
	if !updated {
		err = c.UpdateMachineType(vm, isRunning)
		if err != nil {
			fmt.Println(err)
			return err
		}
	}

	// don't need to do anything else to stopped VMs
	if !isRunning {
		return nil
	}

	// get VMI from cache
	vmKey, err := cache.MetaNamespaceKeyFunc(vm)
	if err != nil {
		fmt.Println(err)
		return err
	}
	obj, exists, err = c.VmiInformer.GetStore().GetByKey(vmKey)
	if err != nil || !exists {
		return err
	}

	vmi := obj.(*v1.VirtualMachineInstance)

	// check if VMI machine type has been updated
	updated, err = isMachineTypeUpdated(vmi)
	if err != nil {
		fmt.Println(err)
		return err
	}

	if !updated {
		fmt.Println("vmi machine type has not been updated")
		return nil
	}

	// mark MachineTypeRestartRequired as false
	patchString := fmt.Sprintf(`[{ "op": "replace", "path": "/status/machineTypeRestartRequired", "value": %t }]`, false)
	err = c.statusUpdater.PatchStatus(vm, types.JSONPatchType, []byte(patchString), &k8sv1.PatchOptions{})
	if err != nil {
		fmt.Println(err)
		return err
	}

	return nil
}

func vmIsRunning(vm *v1.VirtualMachine) (bool, error) {
	runStrategy, err := vm.RunStrategy()
	if err != nil {
		return false, err
	}

	if runStrategy == v1.RunStrategyAlways {
		return true, nil
	}

	return false, nil
}
