package virtctl

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/util"

	//. "kubevirt.io/kubevirt/pkg/virtctl/convertmachinetype"
	. "kubevirt.io/kubevirt/pkg/virtctl/convertmachinetype/massmachinetypetransition"
)

const (
	latestMachineTypeVersion = "pc-q35-rhel9.2.0"
	convertMachineType       = "convert-machine-type"
)

var _ = Describe("[sig-compute][virtctl] mass machine type transition", func() {
	var virtClient kubecli.KubevirtClient
	var err error

	BeforeEach(func() {
		virtClient, err = kubecli.GetKubevirtClient()
		Expect(err).ToNot(HaveOccurred())
	})

	Context("for a single VM", func() {
		var vm *v1.VirtualMachine

		Context("when VM machine type is less than minimum supported version", func() {
			BeforeEach(func() {
				vm = createVM(virtClient, "pc-q35-rhel8.2.0")
			})

			AfterEach(func() {
				err = virtClient.VirtualMachine(util.NamespaceTestDefault).Delete(context.Background(), vm.Name, &metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when VM is not running", func() {
				It("VM machine type should be updated to latest machine type", func() {
					//err = clientcmd.NewRepeatableVirtctlCommand(convertMachineType)()
					//Expect(err).ToNot(HaveOccurred())

					err = UpdateMachineTypes(virtClient)
					Expect(err).ToNot(HaveOccurred())

					vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineTypeVersion))
				})
			})

			Context("when VM is running", func() {

				BeforeEach(func() {
					vm = tests.StartVirtualMachine(vm)

					err = UpdateMachineTypes(virtClient)
					Expect(err).ToNot(HaveOccurred())

					vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
				})

				AfterEach(func() {
					vm = tests.StopVirtualMachine(vm)
				})

				It("VM machine type should be updated to latest machine type", func() {
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineTypeVersion))
				})

				It("should add 'restart-vm-required' label to VM", func() {
					Expect(vm.Labels).To(HaveKeyWithValue("restart-vm-required", "true"))
				})
			})

			/*DescribeTable("when --namespace is", func(namespace string) {
			},
				Entry("equal to VM's namespace, should update VM machine type to latest machine type", "default"),
				Entry("not equal to VM's namespace, should not update VM machine type", "my-namespace"),
			)*/
		})

		Context("when VM machine type is 'q35'", func() {
			BeforeEach(func() {
				vm = createVM(virtClient, "q35")
			})

			AfterEach(func() {
				err = virtClient.VirtualMachine(util.NamespaceTestDefault).Delete(context.Background(), vm.Name, &metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when VM is not running", func() {
				It("VM machine type should remain 'q35'", func() {
					err = UpdateMachineTypes(virtClient)
					Expect(err).ToNot(HaveOccurred())

					vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal("q35"))

				})
			})

			Context("when VM is running", func() {
				BeforeEach(func() {
					vm = tests.StartVirtualMachine(vm)

					err = UpdateMachineTypes(virtClient)
					Expect(err).ToNot(HaveOccurred())

					vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
				})

				AfterEach(func() {
					vm = tests.StopVirtualMachine(vm)
				})

				It("VM machine type should remain 'q35'", func() {
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal("q35"))
				})

				It("should add 'restart-vm-required' label to VM", func() {
					Expect(vm.Labels).To(HaveKeyWithValue("restart-vm-required", "true"))
				})
			})
		})

		DescribeTable("should not update VM machine type when machine type is", func(machineType string) {
			vm = createVM(virtClient, machineType)

			err = UpdateMachineTypes(virtClient)
			Expect(err).ToNot(HaveOccurred())

			vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm.Name, &metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineType))

			err = virtClient.VirtualMachine(util.NamespaceTestDefault).Delete(context.Background(), vm.Name, &metav1.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
		},
			Entry("greater than minimum supported version", "pc-q35-rhel9.2.0"),
			Entry("equal to minimum supported version", "pc-q35-rhel9.0.0"),
		)
	})

	Context("for multiple VMs", func() {
		Context("when all VMs' machine types are less than minimum supported version", func() {
			var vm1 *v1.VirtualMachine
			var vm2 *v1.VirtualMachine
			var vm3 *v1.VirtualMachine
			var vm4 *v1.VirtualMachine

			BeforeEach(func() {
				vm1 = createVM(virtClient, "pc-q35-rhel8.2.0")
				vm2 = createVM(virtClient, "pc-q35-rhel8.2.0")
				vm3 = createVM(virtClient, "pc-q35-rhel8.2.0")
				vm4 = createVM(virtClient, "pc-q35-rhel8.2.0")
			})

			AfterEach(func() {
				err = virtClient.VirtualMachine(util.NamespaceTestDefault).Delete(context.Background(), vm1.Name, &metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())

				err = virtClient.VirtualMachine(util.NamespaceTestDefault).Delete(context.Background(), vm2.Name, &metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())

				err = virtClient.VirtualMachine(util.NamespaceTestDefault).Delete(context.Background(), vm3.Name, &metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())

				err = virtClient.VirtualMachine(util.NamespaceTestDefault).Delete(context.Background(), vm4.Name, &metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when no namespace is specified", func() {
				It("should update all VMs' machine types to latest machine type", func() {
					err = UpdateMachineTypes(virtClient)
					Expect(err).ToNot(HaveOccurred())

					vm, err := virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm1.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineTypeVersion))

					vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm2.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineTypeVersion))

					vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm3.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineTypeVersion))

					vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vm4.Name, &metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineTypeVersion))
				})
			})

			/*Context("when namespace is specified", func() {
				It("should update all VMs with specified namespace to latest machine type", func() {
				})

				It("should not update any VMs with namespaces other than the specified namespace", func() {
				})
			})*/
		})
	})
})

func createVM(virtClient kubecli.KubevirtClient, machineType string) *v1.VirtualMachine {
	template := tests.NewRandomVMI()
	template.Spec.Domain.Machine = &v1.Machine{Type: machineType}
	vm := tests.NewRandomVirtualMachine(template, false)

	vm, err := virtClient.VirtualMachine(util.NamespaceTestDefault).Create(context.Background(), vm)
	Expect(err).ToNot(HaveOccurred())

	vm = tests.StopVirtualMachine(vm)
	return vm
}
