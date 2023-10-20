package convertmachinetype

import (
	"context"
	"fmt"
	"time"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

type JobController struct {
	VmInformer cache.SharedIndexInformer
	VirtClient kubecli.KubevirtClient
	Queue      workqueue.RateLimitingInterface
	ExitJob    chan struct{}
}

func NewJobController(
	vmInformer cache.SharedIndexInformer,
	virtClient kubecli.KubevirtClient,
) (*JobController, error) {
	c := &JobController{
		VmInformer: vmInformer,
		VirtClient: virtClient,
		Queue:      workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		ExitJob:    make(chan struct{}),
	}

	_, err := vmInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				c.Queue.Add(key)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)
			if err == nil {
				c.Queue.Add(key)
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err == nil {
				c.Queue.Add(key)
			}
		},
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

func (c *JobController) numVmsPendingUpdate() int {
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

	fmt.Println("Starting job controller")
	go c.VmInformer.Run(stopCh)

	if !cache.WaitForCacheSync(make(<-chan struct{}), c.VmInformer.HasSynced) {
		fmt.Println("Timed out waiting for caches to sync")
		return
	}

	wait.Until(c.runWorker, time.Second, stopCh)
}

func (c *JobController) runWorker() {
	for c.Execute() {

	}
}

func (c *JobController) Execute() bool {
	key, quit := c.Queue.Get()
	if quit {
		return false
	}

	defer c.Queue.Done(key)
	err := c.execute(key.(string))

	if err != nil {
		c.Queue.AddRateLimited(key)
	} else {
		c.Queue.Forget(key)
	}

	return true
}

func (c *JobController) execute(key string) error {
	obj, exists, err := c.VmInformer.GetStore().GetByKey(key)
	if !exists || err != nil {
		return err
	}

	vm, ok := obj.(*k6tv1.VirtualMachine)
	if !ok {
		return fmt.Errorf("Unexpected resource %+v", obj)
	}

	c.handleVM(vm.DeepCopy())

	return nil
}

func (c *JobController) handleVM(vm *k6tv1.VirtualMachine) {
	// check if VM has restart-vm-required label
	if _, ok := vm.Labels["restart-vm-required"]; !ok {
		return
	}

	// check that VM machine type has been updated
	updated := isMachineTypeUpdated(vm)
	if !updated {
		return
	}

	// for running VMs, check that machine type in VMI status no longer
	// matches glob
	if (vm.Spec.Running != nil && *vm.Spec.Running) || vm.Spec.RunStrategy != nil {
		vmi, err := c.VirtClient.VirtualMachineInstance(vm.Namespace).Get(context.Background(), vm.Name, &k8sv1.GetOptions{})
		if err != nil {
			fmt.Println(err)
			return
		}

		if vmi.Status.Phase != k6tv1.Running {
			fmt.Printf("VMI %s is not in Running phase\n", vmi.Name)
			return
		}

		match, err := matchMachineType(vmi.Status.Machine.Type)
		if match || err != nil {
			fmt.Println(err)
			return
		}
	}

	// remove warning label from VM
	err := c.removeWarningLabel(vm)
	if err != nil {
		fmt.Println(err)
	}

	numVmsPendingUpdate := c.numVmsPendingUpdate()
	fmt.Printf("VMIs pending update: %d\n", numVmsPendingUpdate)
	if numVmsPendingUpdate <= 0 {
		close(c.ExitJob)
	}
}
