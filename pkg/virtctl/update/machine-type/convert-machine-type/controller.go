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

var exitJob chan struct{}

type JobController struct {
	VmiInformer cache.SharedIndexInformer
	VirtClient  kubecli.KubevirtClient
	Queue       workqueue.RateLimitingInterface
}

func NewJobController(
	vmiInformer cache.SharedIndexInformer,
	virtClient kubecli.KubevirtClient,
) (*JobController, error) {
	c := &JobController{
		VmiInformer: vmiInformer,
		VirtClient:  virtClient,
		Queue:       workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
	}

	_, err := vmiInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
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

func (c *JobController) removeWarningLabel(vm *k6tv1.VirtualMachine) error {
	removeLabel := `[{"op": "remove", "path": "/metadata/labels/restart-vm-required"}]`
	vm, err := c.VirtClient.VirtualMachine(vm.Namespace).Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(removeLabel), &k8sv1.PatchOptions{})
	return err
}

func (c *JobController) numVmisPendingUpdate() int {
	vmList, err := c.VirtClient.VirtualMachine(namespace).List(context.Background(), &k8sv1.ListOptions{
		LabelSelector: `restart-vm-required=""`,
	})
	if err != nil {
		fmt.Println(err)
		return -1
	}

	return len(vmList.Items)
}

func (c *JobController) run(stopCh <-chan struct{}) {
	defer c.Queue.ShutDown()

	fmt.Print("Starting job controller")
	go c.VmiInformer.Run(stopCh)

	if !cache.WaitForCacheSync(stopCh, c.VmiInformer.HasSynced) {
		fmt.Print("Timed out waiting for caches to sync")
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
	obj, exists, err := c.VmiInformer.GetIndexer().GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		c.handleDeletedVmi(obj)
	}

	return nil
}

func (c *JobController) handleDeletedVmi(obj interface{}) {
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
	updated := IsMachineTypeUpdated(vm)
	if err != nil {
		fmt.Print(err)
		return
	}

	if !updated {
		return
	}

	// remove warning label from VM
	err = c.removeWarningLabel(vm)
	if err != nil {
		fmt.Println(err)
	}

	// ensure all 'restart-vm-required' labels on affected VMs
	// are cleared using label selector
	numVmisPendingUpdate := c.numVmisPendingUpdate()
	if numVmisPendingUpdate != 0 {
		return
	}

	close(exitJob)
}
