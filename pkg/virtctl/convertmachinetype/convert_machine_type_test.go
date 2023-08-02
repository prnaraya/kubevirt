package convertmachinetype_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spf13/cobra"

	"kubevirt.io/kubevirt/pkg/virtctl/convertmachinetype"
	"kubevirt.io/kubevirt/tests/clientcmd"
)

var _ = Describe("Convert machine type", func() {

	Describe("should create convert-machine-type job", func() {

		DescribeTable("should remove 'restart-vm-required' label from VM", func(flag int) {
			var cmd *cobra.Command

			switch flag {
			case 0:
				cmd = clientcmd.NewVirtctlCommand(convertmachinetype.COMMAND_CONVERT_MACHINE_TYPE)
			case 1:
				cmd = clientcmd.NewVirtctlCommand(convertmachinetype.COMMAND_CONVERT_MACHINE_TYPE, "--namespace=default")
			case 2:
				cmd = clientcmd.NewVirtctlCommand(convertmachinetype.COMMAND_CONVERT_MACHINE_TYPE, "--force-restart=true")
			case 3:
				cmd = clientcmd.NewVirtctlCommand(convertmachinetype.COMMAND_CONVERT_MACHINE_TYPE, "--label-selector=kubevirt.io/schedulable=true")
			}
			Expect(cmd.Execute()).To(Succeed())
		},
			Entry("", 0),
			Entry("with namespace", 1),
			Entry("with force-restart=true", 2),
			Entry("with label-selector", 3),
		)
	})
})
