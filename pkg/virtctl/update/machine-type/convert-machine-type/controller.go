package convertmachinetype

import (
	"context"
	"fmt"
	"strings"
	"time"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		AddFunc:    c.genericEventHandler,
		UpdateFunc: func(_, newObj interface{}) { c.genericEventHandler(newObj) },
		DeleteFunc: c.genericEventHandler,
	})
	if err != nil {
		return nil, err
	}
	_, err = vmiInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.genericEventHandler,
		DeleteFunc: c.genericEventHandler,
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
	_, exists, err := c.VmiInformer.GetStore().GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		namespace, name := parseKey(key)
		c.handleDeletedVmi(namespace, name)
	}

	return nil
}

func (c *JobController) genericEventHandler(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err == nil {
		c.Queue.Add(key)
	}
}

func (c *JobController) handleDeletedVmi(namespace, name string) {
	// get VM from cache
	vmKey := fmt.Sprintf("%s/%s", namespace, name)
	obj, exists, err := c.VmInformer.GetStore().GetByKey(vmKey)
	if !exists || err != nil {
		fmt.Println(err)
		return
	}
	vm := obj.(*k6tv1.VirtualMachine)
	// check if VM has restart-vm-required label
	if _, ok := vm.Labels["restart-vm-required"]; !ok {
		fmt.Println("vm does not have restart label")
		return
	}

	// check that VM machine type is not outdated in the case that a VM
	// has been restarted for a different reason before its machine type
	// has been updated
	updated := isMachineTypeUpdated(vm)
	if err != nil {
		fmt.Println(err)
		return
	}

	if !updated {
		fmt.Println("vm not updated")
		return
	}

	// isRunning, err := vmIsRunning(vm)
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }

	// if isRunning {
	// 	obj, exists, err = c.VmiInformer.GetStore().GetByKey(vmKey)
	// }

	// remove warning label from VM
	err = c.removeWarningLabel(vm)
	if err != nil {
		fmt.Println(err)
	}

	numVmisPendingUpdate := c.numVmisPendingUpdate()
	fmt.Printf("checking num vmis after vmi is deleted: %d\n", numVmisPendingUpdate)
	if numVmisPendingUpdate <= 0 {
		close(c.ExitJob)
	}
}

func parseKey(key string) (namespace, name string) {
	keySubstrings := strings.Split(key, "/")
	return keySubstrings[0], keySubstrings[1]
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
