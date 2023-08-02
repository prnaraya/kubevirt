package massmachinetypetransition_test

import (
	"context"
	"fmt"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	. "kubevirt.io/kubevirt/pkg/virtctl/convertmachinetype/massmachinetypetransition"
)

var _ = Describe("Informers", func() {
	var ctrl *gomock.Controller
	var virtClient *kubecli.MockKubevirtClient
	var vmInterface *kubecli.MockVirtualMachineInterface
	var vmiInterface *kubecli.MockVirtualMachineInstanceInterface
	var kubeClient *fake.Clientset

	shouldExpectRemoveLabel := func(vm *k6tv1.VirtualMachine) {
		patchData := `[{"op": "remove", "path": "/metadata/labels/restart-vm-required"}]`

		vmInterface.EXPECT().Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(patchData), &v1.PatchOptions{}).Times(1)

		kubeClient.Fake.PrependReactor("patch", "virtualmachines", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			patch, ok := action.(testing.PatchAction)
			Expect(ok).To(BeTrue())
			Expect(patch.GetPatch()).To(Equal([]byte(patchData)))
			Expect(patch.GetPatchType()).To(Equal(types.MergePatchType))
			Expect(vm.Labels).ToNot(HaveKeyWithValue("restart-vm-required", "true"), "should remove `restart-vm-required` label")
			return true, vm, nil
		})
	}

	Describe("HandleDeletedVMI when VMI is deleted", func() {
		var vm *k6tv1.VirtualMachine
		var vm2 *k6tv1.VirtualMachine
		var vmi *k6tv1.VirtualMachineInstance
		var vmi2 *k6tv1.VirtualMachineInstance
		var vmKey string
		var vmKey2 string
		var err error

		shouldExpectGetVM := func(vm *k6tv1.VirtualMachine) {
			vmInterface.EXPECT().Get(context.Background(), vm.Name, &v1.GetOptions{}).Return(vm, nil).Times(1)

			kubeClient.Fake.PrependReactor("get", "virtualmachines", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
				get, ok := action.(testing.GetAction)
				Expect(ok).To(BeTrue())
				Expect(get.GetNamespace()).To(Equal(v1.NamespaceDefault))
				Expect(get.GetName()).To(Equal(vm.Name))
				return true, vm, nil
			})
		}

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			virtClient = kubecli.NewMockKubevirtClient(ctrl)
			vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)
			vmiInterface = kubecli.NewMockVirtualMachineInstanceInterface(ctrl)
			kubeClient = fake.NewSimpleClientset()

			virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()
			virtClient.EXPECT().VirtualMachineInstance(gomock.Any()).Return(vmiInterface).AnyTimes()

			vm = newVMWithMachineType(unsupportedMachineType, false)
			vm.Labels["restart-vm-required"] = "true"
			vm2 = newVMWithMachineType(unsupportedMachineType, true)
			vm2.Name = vm.Name + "-2"
			vm2.Labels["restart-vm-required"] = "true"

			vmi = newVMIWithMachineType(unsupportedMachineType, vm.Name)
			vmi2 = newVMIWithMachineType(unsupportedMachineType, vm2.Name)

			vmKey, err = cache.MetaNamespaceKeyFunc(vm)
			Expect(err).ToNot(HaveOccurred())
			VmisPendingUpdate[vmKey] = struct{}{}
			vmKey2, err = cache.MetaNamespaceKeyFunc(vm2)
			Expect(err).ToNot(HaveOccurred())
			VmisPendingUpdate[vmKey2] = struct{}{}
		})

		Context("when VM machine type has been updated", func() {
			It("should remove VM from VmisPendingUpdate list", func() {
				vm.Spec.Template.Spec.Domain.Machine.Type = fmt.Sprintf("pc-q35-%s", LatestMachineTypeVersion)

				shouldExpectGetVM(vm)
				shouldExpectRemoveLabel(vm)

				HandleDeletedVmi(vmi)
				Expect(VmisPendingUpdate).ToNot(HaveKey(vmKey))
				delete(VmisPendingUpdate, vmKey2)
			})
		})

		Context("when VM machine type has not been updated", func() {
			It("should not remove VM from VmisPendingUpdate list", func() {
				shouldExpectGetVM(vm)

				HandleDeletedVmi(vmi)
				Expect(VmisPendingUpdate).To(HaveKey(vmKey))
				delete(VmisPendingUpdate, vmKey)
				delete(VmisPendingUpdate, vmKey2)
			})
		})

		Context("when final VM is removed from VmisPendingUpdate list", func() {

			It("should signal job shutdown when no VMs remain with `restart-vm-required`", func() {
				vm.Spec.Template.Spec.Domain.Machine.Type = fmt.Sprintf("pc-q35-%s", LatestMachineTypeVersion)
				vm2.Spec.Template.Spec.Domain.Machine.Type = fmt.Sprintf("pc-q35-%s", LatestMachineTypeVersion)
				vm2.Spec.Running = pointer.Bool(false)

				shouldExpectGetVM(vm)
				shouldExpectGetVM(vm2)
				shouldExpectRemoveLabel(vm)
				shouldExpectRemoveLabel(vm2)

				vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{
					LabelSelector: "restart-vm-required=true",
				}).Return(&k6tv1.VirtualMachineList{
					Items: []k6tv1.VirtualMachine{},
				}, nil).Times(1)

				HandleDeletedVmi(vmi)
				HandleDeletedVmi(vmi2)
				Expect(VmisPendingUpdate).To(BeEmpty())
				Expect(GetExitJobChannel()).To(BeClosed())
			})

			Context("if there exists VMs with `restart-vm-required` label", func() {
				It("should not signal job shutdown", func() {
					vm.Spec.Template.Spec.Domain.Machine.Type = fmt.Sprintf("pc-q35-%s", LatestMachineTypeVersion)
					delete(VmisPendingUpdate, vmKey2)

					shouldExpectGetVM(vm)
					shouldExpectRemoveLabel(vm)

					vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{
						LabelSelector: "restart-vm-required=true",
					}).Return(&k6tv1.VirtualMachineList{
						Items: []k6tv1.VirtualMachine{*vm2},
					}, nil).Times(1)

					HandleDeletedVmi(vmi)
					Expect(GetExitJobChannel()).ToNot(BeClosed())
					Expect(VmisPendingUpdate).To(HaveKey(vmKey2), "should add VMs with label back to VmisPendingUpdate list")
				})
			})
		})
	})

	Describe("RemoveWarningLabel", func() {

		It("should remove 'restart-vm-required' label from VM", func() {
			ctrl = gomock.NewController(GinkgoT())
			virtClient = kubecli.NewMockKubevirtClient(ctrl)
			vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)
			virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()

			testVM := newVmWithLabel()

			shouldExpectRemoveLabel(testVM)

			err := RemoveWarningLabel(virtClient, testVM)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

func newVmWithLabel() *k6tv1.VirtualMachine {
	testVM := &k6tv1.VirtualMachine{
		ObjectMeta: k8sv1.ObjectMeta{
			Name:      "test-vm",
			Namespace: k8sv1.NamespaceDefault,
			Labels:    map[string]string{"restart-vm-required": "true"},
		},
	}
	return testVM
}
