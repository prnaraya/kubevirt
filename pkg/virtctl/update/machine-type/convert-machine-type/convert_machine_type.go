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
	machineTypeGlob = machineTypeEnv

	restartEnv, exists := os.LookupEnv("FORCE_RESTART")
	if exists {
		restartNow, err = strconv.ParseBool(restartEnv)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	namespaceEnv, exists := os.LookupEnv("NAMESPACE")
	if exists && namespaceEnv != "" {
		namespace = namespaceEnv
	}

	selectorEnv, exists := os.LookupEnv("LABEL_SELECTOR")
	if exists {
		labelSelector = selectorEnv
	}

	// set up JobController
	virtCli, err := getVirtCli()
	if err != nil {
		os.Exit(1)
	}

	listWatcher := cache.NewListWatchFromClient(virtCli.RestClient(), "virtualmachineinstances", namespace, fields.Everything())
	vmiInformer := cache.NewSharedIndexInformer(listWatcher, &k6tv1.VirtualMachineInstance{}, 1*time.Hour, cache.Indexers{})

	exitJob := make(chan struct{})

	controller, err := NewJobController(vmiInformer, virtCli)
	if err != nil {
		os.Exit(1)
	}

	go controller.run(exitJob)

	err = controller.UpdateMachineTypes()
	if err != nil {
		fmt.Println(err)
	}

	numVmisPendingUpdate := controller.numVmisPendingUpdate()
	if numVmisPendingUpdate <= 0 {
		close(exitJob)
	}

	<-exitJob

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
