package main

import (
	convertmachinetype "kubevirt.io/kubevirt/pkg/virtctl/convert-machine-type/convert-machine-type-job"
)

func main() {
	convertmachinetype.Run()
}
