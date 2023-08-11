package convertmachinetypejob

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
	k6tv1 "kubevirt.io/api/core/v1"
)

func Run() {
	// check env variables and set them accordingly
	var err error
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

	controller, err := NewJobController(vmiInformer, virtCli, exitJob)
	if err != nil {
		os.Exit(1)
	}

	go controller.VmiInformer.Run(controller.ExitJob)

	err = controller.UpdateMachineTypes()
	if err != nil {
		fmt.Println(err)
	}

	numVmisPendingUpdate := controller.numVmisPendingUpdate()
	if numVmisPendingUpdate == 0 {
		close(controller.ExitJob)
	}

	<-controller.ExitJob

	os.Exit(0)
}
