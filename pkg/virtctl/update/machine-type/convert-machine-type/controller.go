package convertmachinetype

import (
	"context"
	"fmt"
	"time"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	k6tv1 "kubevirt.io/api/core/v1"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

type JobController struct {
	VmInformer  cache.SharedIndexInformer
	VmiInformer cache.SharedIndexInformer
	VirtClient  kubecli.KubevirtClient
	Queue       workqueue.RateLimitingInterface
	ExitJob     chan struct{}
}

func NewJobController(
	vmInformer, vmiInformer cache.SharedIndexInformer,
	virtClient kubecli.KubevirtClient,
) (*JobController, error) {
	c := &JobController{
		VmInformer:  vmInformer,
		VmiInformer: vmiInformer,
		VirtClient:  virtClient,
		Queue:       workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		ExitJob:     make(chan struct{}),
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

func (c *JobController) removeWarningLabel(vm *k6tv1.VirtualMachine) error {
	removeLabel := `[{"op": "remove", "path": "/metadata/labels/restart-vm-required"}]`
	vm, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(removeLabel), &k8sv1.PatchOptions{})
	if err != nil {
		return err
	}

	return nil
}

func (c *JobController) numVmisPendingUpdate() int {
	vmList, err := c.VirtClient.VirtualMachine(Namespace).List(context.Background(), &k8sv1.ListOptions{
		LabelSelector: "restart-vm-required=",
	})
	if err != nil {
		fmt.Println(err)
		return -1
	}

	return len(vmList.Items)
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

	wait.Until(c.runWorker, time.Second, stopCh)
}

func (c *JobController) runWorker() {
	for c.Execute() {
		if c.numVmisPendingUpdate() <= 0 {
			close(c.ExitJob)
		}
	}
}

func (c *JobController) Execute() bool {
	key, quit := c.Queue.Get()
	if quit {
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

	// we only care if the VM has the specified namespace and label(s)
	if vm.Namespace != Namespace {
		return nil
	}

	if !LabelSelector.Matches(labels.Set(vm.Labels)) {
		return nil
	}

	// check if VM is running
	isRunning, err := vmIsRunning(vm)
	if err != nil {
		fmt.Println(err)
		return err
	}

	// check if VM machine type was updated
	updated, err := isMachineTypeUpdated(vm)
	if err != nil {
		return err
	}

	// update non-running VMs that require update
	if !updated && !isRunning {
		err = c.UpdateMachineType(vm, false)
		return err
	}

	if isRunning {
		// get VMI from cache
		vmKey, err := cache.MetaNamespaceKeyFunc(vm)
		if err != nil {
			return err
		}
		obj, exists, err := c.VmiInformer.GetStore().GetByKey(vmKey)
		if err != nil || !exists {
			return err
		}

		vmi := obj.(*v1.VirtualMachineInstance)

		// update VM machine type if it needs to be
		if !updated {
			err = c.UpdateMachineType(vm.DeepCopy(), true)
			return err
		}

		// check if VMI machine type has been updated
		updated, err = isMachineTypeUpdated(vmi)
		if err != nil {
			return err
		}

		if !updated {
			fmt.Println("vmi machine type has not been updated")
			return nil
		}
	}

	// check if VM has restart-vm-required label before trying
	// to remove it
	if _, ok := vm.Labels["restart-vm-required"]; ok {
		// remove warning label from VM
		err = c.removeWarningLabel(vm)
		return err
	}

	return nil
}

func vmIsRunning(vm *v1.VirtualMachine) (bool, error) {
	runStrategy, err := vm.RunStrategy()
	if err != nil {
		return false, err
	}

	switch runStrategy {
	case v1.RunStrategyHalted, v1.RunStrategyManual, v1.RunStrategyOnce:
		return false, nil
	default:
		return true, nil
	}
}
