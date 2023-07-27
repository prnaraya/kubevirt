package massmachinetypetransition

import (
	"fmt"
	"os"
	"strconv"
)

func Run() {
	var err error
	// update restartNow if env is set
	restartEnv, exists := os.LookupEnv("FORCE_RESTART")
	if exists {
		restartNow, err = strconv.ParseBool(restartEnv)
		if err != nil {
			fmt.Println(err)
		}
	}

	// update namespace if env is set
	namespaceEnv, exists := os.LookupEnv("NAMESPACE")
	if exists && namespaceEnv != "" {
		namespace = namespaceEnv
	}

	// update label selector if env is set
	selectorEnv, exists := os.LookupEnv("LABEL_SELECTOR")
	if exists {
		labelSelector = selectorEnv
	}

	virtCli, err := getVirtCli()
	if err != nil {
		os.Exit(1)
	}

	vmiInformer, err := getVmiInformer(virtCli)
	if err != nil {
		os.Exit(1)
	}

	go vmiInformer.Run(exitJob)

	err = UpdateMachineTypes(virtCli)
	if err != nil {
		fmt.Println(err)
	}

	// wait for list of VMIs that need restart to be empty
	<-exitJob

	os.Exit(0)
}
