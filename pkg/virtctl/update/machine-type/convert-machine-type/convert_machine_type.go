package convertmachinetype

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"
)

func Run() {
	// check env variables and set them accordingly
	var err error

	machineTypeEnv, exists := os.LookupEnv("MACHINE_TYPE")
	if !exists {
		fmt.Println("No machine type was specified.")
		os.Exit(1)
	}
	MachineTypeGlob = machineTypeEnv

	restartEnv, exists := os.LookupEnv("FORCE_RESTART")
	if exists {
		RestartNow, err = strconv.ParseBool(restartEnv)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	namespaceEnv, exists := os.LookupEnv("NAMESPACE")
	if exists && namespaceEnv != "" {
		Namespace = namespaceEnv
	}

	selectorEnv, exists := os.LookupEnv("LABEL_SELECTOR")
	if exists {
		LabelSelector = selectorEnv
	}

	// set up JobController
	virtCli, err := getVirtCli()
	if err != nil {
		os.Exit(1)
	}

	vmListWatcher := cache.NewListWatchFromClient(virtCli.RestClient(), "virtualmachines", Namespace, fields.Everything())
	vmInformer := cache.NewSharedIndexInformer(vmListWatcher, &k6tv1.VirtualMachine{}, 1*time.Hour, cache.Indexers{})
	vmiListWatcher := cache.NewListWatchFromClient(virtCli.RestClient(), "virtualmachineinstances", Namespace, fields.Everything())
	vmiInformer := cache.NewSharedIndexInformer(vmiListWatcher, &k6tv1.VirtualMachineInstance{}, 1*time.Hour, cache.Indexers{})

	controller, err := NewJobController(vmInformer, vmiInformer, virtCli)
	if err != nil {
		os.Exit(1)
	}

	go controller.run(controller.ExitJob)

	// // if no running VMs have been updated or need to be
	// // restarted by this point, job can terminate
	// numVmisPendingUpdate := controller.numVmisPendingUpdate()
	// fmt.Printf("checking num vmis after updateMachineTypes runs: %d\n", numVmisPendingUpdate)
	// if numVmisPendingUpdate <= 0 {
	// 	close(controller.ExitJob)
	// }

	<-controller.ExitJob

	os.Exit(0)
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
