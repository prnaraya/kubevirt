package convertmachinetype_test

import (
	"context"
	"fmt"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/pkg/testutils"
	. "kubevirt.io/kubevirt/pkg/virtctl/update/machine-type/convert-machine-type"
)

var _ = Describe("JobController", func() {
	var ctrl *gomock.Controller
	var virtClient *kubecli.MockKubevirtClient
	var vmInterface *kubecli.MockVirtualMachineInterface
	var vmiInterface *kubecli.MockVirtualMachineInstanceInterface
	var kubeClient *fake.Clientset
	var vmInformer cache.SharedIndexInformer
	var mockQueue *testutils.MockWorkQueue
	var controller *JobController
	var err error

	shouldExpectGetVMI := func(vmi *virtv1.VirtualMachineInstance) {
		vmiInterface.EXPECT().Get(context.Background(), vmi.Name, &metav1.GetOptions{}).Return(vmi, nil).Times(1)
	}

	addVM := func(vm *virtv1.VirtualMachine) {
		key, err := cache.MetaNamespaceKeyFunc(vm)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		mockQueue.Add(key)
		err = vmInformer.GetStore().Add(vm)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}

	updateVM := func(vm *virtv1.VirtualMachine) {
		key, err := cache.MetaNamespaceKeyFunc(vm)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())

		mockQueue.Add(key)
		err = vmInformer.GetStore().Update(vm)
		ExpectWithOffset(1, err).ToNot(HaveOccurred())
	}

	shouldUpdateVMRunningSpec := func(vm *virtv1.VirtualMachine) {
		kubeClient.Fake.PrependReactor("update", "virtualmachines", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			update, ok := action.(testing.UpdateAction)
			Expect(ok).To(BeTrue())
			updatedVM, ok := update.GetObject().(*virtv1.VirtualMachine)
			Expect(ok).To(BeTrue())
			Expect(updatedVM.Namespace).To(Equal(vm.Namespace))
			Expect(updatedVM.Name).To(Equal(vm.Name))
			Expect(updatedVM.Spec.Running).To(HaveValue(BeFalse()))
			return true, updatedVM, nil
		})
		vmInterface.EXPECT().Update(gomock.Any(), gomock.Any()).Return(vm, nil)
		updateVM(vm)
	}

	shouldExpectVMICreation := func(vmi *virtv1.VirtualMachineInstance) {
		kubeClient.Fake.PrependReactor("create", "virtualmachineinstances", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			create, ok := action.(testing.CreateAction)
			Expect(ok).To(BeTrue())
			Expect(create.GetObject().(*virtv1.VirtualMachineInstance).Name).To(Equal(vmi.Name))

			return true, create.GetObject(), nil
		})
	}

	shouldExpectVMIDeletion := func(vmi *virtv1.VirtualMachineInstance, vm *virtv1.VirtualMachine) *virtv1.VirtualMachine {
		kubeClient.Fake.PrependReactor("delete", "virtualmachineinstances", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			delete, ok := action.(testing.DeleteAction)
			Expect(ok).To(BeTrue())
			Expect(delete.GetName()).To(Equal(vmi.Name))

			return true, nil, nil
		})

		vm.Spec.Running = pointer.Bool(false)
		shouldUpdateVMRunningSpec(vm)
		vm, err = virtClient.VirtualMachine(vm.Namespace).Update(context.Background(), vm)
		return vm
	}

	shouldExpectRemoveLabel := func(vm *virtv1.VirtualMachine) {
		patchData := `[{"op": "remove", "path": "/metadata/labels/restart-vm-required"}]`

		vmInterface.EXPECT().Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(patchData), &metav1.PatchOptions{}).DoAndReturn(func(ctx context.Context, name string, patchType types.PatchType, data []byte, opts *metav1.PatchOptions, subresources ...interface{}) (*virtv1.VirtualMachine, error) {
			delete(vm.Labels, "restart-vm-required")
			return vm, nil
		}).Times(1)
	}

	Describe("When VMI is deleted", func() {
		var vm *virtv1.VirtualMachine
		var vmi *virtv1.VirtualMachineInstance

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			virtClient = kubecli.NewMockKubevirtClient(ctrl)
			vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)
			vmiInterface = kubecli.NewMockVirtualMachineInstanceInterface(ctrl)
			kubeClient = fake.NewSimpleClientset()

			vmInformer, _ = testutils.NewFakeInformerFor(&virtv1.VirtualMachineInstance{})

			controller, err = NewJobController(vmInformer, virtClient)
			Expect(err).ToNot(HaveOccurred())

			mockQueue = testutils.NewMockWorkQueue(controller.Queue)
			controller.Queue = mockQueue

			go controller.VmInformer.Run(controller.ExitJob)
			Expect(cache.WaitForCacheSync(controller.ExitJob, controller.VmInformer.HasSynced)).To(BeTrue())

			virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()
			virtClient.EXPECT().VirtualMachineInstance(gomock.Any()).Return(vmiInterface).AnyTimes()
		})

		Context("if VM machine type has not been updated", func() {
			It("should not remove `restart-vm-required` label from VM", func() {
				vm = newVMWithRestartLabel(machineTypeNeedsUpdate)
				vmi = newVMIWithMachineType(machineTypeNeedsUpdate, vm.Name)
				addVM(vm)

				shouldExpectGetVMI(vmi)
				shouldExpectVMICreation(vmi)
				shouldExpectVMIDeletion(vmi, vm)

				controller.Execute()

				Expect(vm.Labels).To(HaveKey("restart-vm-required"))
				Expect(controller.ExitJob).ToNot(BeClosed(), "should not signal job termination as long as labels exist")
			})
		})

		Context("if VM machine type has been updated", func() {
			It("should remove `restart-vm-required` label from VM", func() {
				selector, _ := labels.Parse("restart-vm-required=")
				fmt.Println(selector.String())
				vm = newVMWithRestartLabel("")
				vmi = newVMIWithMachineType(machineTypeNoUpdate, vm.Name)
				addVM(vm)

				shouldExpectGetVMI(vmi)
				shouldExpectVMICreation(vmi)
				vmInterface.EXPECT().List(context.Background(), &metav1.ListOptions{
					LabelSelector: `restart-vm-required=`,
				}).Return(&virtv1.VirtualMachineList{
					Items: []virtv1.VirtualMachine{},
				}, nil).Times(1)

				shouldExpectVMIDeletion(vmi, vm)
				shouldExpectRemoveLabel(vm)

				controller.Execute()
				Expect(controller.ExitJob).To(BeClosed(), "should signal job termination when no VMs with 'restart-vm-required' label remain")
			})
		})
	})
})

func newVMWithRestartLabel(machineType string) *virtv1.VirtualMachine {
	testVM := &virtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("test-vm-%s", machineType),
			Namespace: metav1.NamespaceDefault,
			Labels:    map[string]string{"restart-vm-required": ""},
		},
		Spec: virtv1.VirtualMachineSpec{
			Running: pointer.Bool(true),
			Template: &virtv1.VirtualMachineInstanceTemplateSpec{
				Spec: virtv1.VirtualMachineInstanceSpec{
					Domain: virtv1.DomainSpec{},
				},
			},
		},
	}
	if machineType != "" {
		testVM.Spec.Template.Spec.Domain.Machine = &virtv1.Machine{Type: machineType}
	}

	return testVM
}
