package convertmachinetype_test

import (
	"context"
	"fmt"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	framework "k8s.io/client-go/tools/cache/testing"
	"k8s.io/utils/pointer"
	k6tv1 "kubevirt.io/api/core/v1"
	virtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/pkg/testutils"
	. "kubevirt.io/kubevirt/pkg/virtctl/update/machine-type/convert-machine-type"
)

var _ = Describe("Informers", func() {
	var ctrl *gomock.Controller
	var virtClient *kubecli.MockKubevirtClient
	var vmInterface *kubecli.MockVirtualMachineInterface
	var vmiInterface *kubecli.MockVirtualMachineInstanceInterface
	var kubeClient *fake.Clientset
	var vmiInformer cache.SharedIndexInformer
	var vmiSource *framework.FakeControllerSource
	var mockQueue *testutils.MockWorkQueue
	var vmiFeeder *testutils.VirtualMachineFeeder
	var controller *JobController
	var err error

	shouldExpectGetVM := func(vm *virtv1.VirtualMachine) {
		vmInterface.EXPECT().Get(context.Background(), vm.Name, &metav1.GetOptions{}).Return(vm, nil).Times(1)

		kubeClient.Fake.PrependReactor("get", "virtualmachines", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			get, ok := action.(testing.GetAction)
			Expect(ok).To(BeTrue())
			Expect(get.GetNamespace()).To(Equal(metav1.NamespaceDefault))
			Expect(get.GetName()).To(Equal(vm.Name))
			return true, vm, nil
		})
	}

	shouldExpectVMICreation := func(vmi *virtv1.VirtualMachineInstance) {
		kubeClient.Fake.PrependReactor("create", "virtualmachineinstances", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			create, ok := action.(testing.CreateAction)
			Expect(ok).To(BeTrue())
			Expect(create.GetObject().(*virtv1.VirtualMachineInstance).Name).To(Equal(vmi.Name))

			return true, create.GetObject(), nil
		})
	}

	shouldExpectVMIDeletion := func(vmi *virtv1.VirtualMachineInstance) {
		kubeClient.Fake.PrependReactor("delete", "virtualmachineinstances", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			delete, ok := action.(testing.DeleteAction)
			Expect(ok).To(BeTrue())
			Expect(delete.GetName()).To(Equal(vmi.Name))

			return true, nil, nil
		})
	}

	shouldExpectRemoveLabel := func(vm *virtv1.VirtualMachine) {
		patchData := `[{"op": "remove", "path": "/metadata/labels/restart-vm-required"}]`

		vmInterface.EXPECT().Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(patchData), &metav1.PatchOptions{}).Times(1)

		kubeClient.Fake.PrependReactor("patch", "virtualmachines", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			patch, ok := action.(testing.PatchAction)
			Expect(ok).To(BeTrue())
			Expect(patch.GetPatch()).To(Equal([]byte(patchData)))
			Expect(patch.GetPatchType()).To(Equal(types.MergePatchType))
			Expect(vm.Labels).ToNot(HaveKey("restart-vm-required"), "should remove `restart-vm-required` label")
			return true, vm, nil
		})
	}

	Describe("When VMI is deleted", func() {
		var vm *k6tv1.VirtualMachine
		var vmi *k6tv1.VirtualMachineInstance

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			virtClient = kubecli.NewMockKubevirtClient(ctrl)
			vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)
			vmiInterface = kubecli.NewMockVirtualMachineInstanceInterface(ctrl)
			kubeClient = fake.NewSimpleClientset()

			vmiInformer, vmiSource = testutils.NewFakeInformerFor(&virtv1.VirtualMachineInstance{})

			controller, err = NewJobController(vmiInformer, virtClient)
			Expect(err).ToNot(HaveOccurred())

			mockQueue = testutils.NewMockWorkQueue(controller.Queue)
			controller.Queue = mockQueue
			vmiFeeder = testutils.NewVirtualMachineFeeder(mockQueue, vmiSource)

			go controller.VmiInformer.Run(controller.ExitJob)
			Expect(cache.WaitForCacheSync(controller.ExitJob, controller.VmiInformer.HasSynced)).To(BeTrue())

			virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()
			virtClient.EXPECT().VirtualMachineInstance(gomock.Any()).Return(vmiInterface).AnyTimes()
		})

		AfterEach(func() {
			close(controller.ExitJob)
		})

		Context("if VM machine type has not been updated", func() {
			It("should not remove `restart-vm-required` label from VM", func() {
				vm = newVMWithRestartLabel(machineTypeNeedsUpdate)
				vmi = newVMIWithMachineType(machineTypeNeedsUpdate, vm.Name)
				vmiFeeder.Add(vmi)

				shouldExpectGetVM(vm)
				shouldExpectVMICreation(vmi)
				shouldExpectVMIDeletion(vmi)

				vmiFeeder.Delete(vmi)
				controller.Execute()

				Expect(vm.Labels).To(HaveKey("restart-vm-required"))
			})
		})

		Context("if VM machine type has been updated", func() {
			It("should remove `restart-vm-required` label from VM", func() {
				vm = newVMWithRestartLabel("")
				vmi = newVMIWithMachineType(machineTypeNoUpdate, vm.Name)
				vmiFeeder.Add(vmi)

				shouldExpectGetVM(vm)
				shouldExpectVMICreation(vmi)
				vmInterface.EXPECT().List(context.Background(), &metav1.ListOptions{
					LabelSelector: `restart-vm-required=""`,
				}).Return(&virtv1.VirtualMachineList{
					Items: []virtv1.VirtualMachine{},
				}, nil).Times(1)

				shouldExpectVMIDeletion(vmi)
				shouldExpectRemoveLabel(vm)

				vmiFeeder.Delete(vmi)
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
			Labels:    map[string]string{"restart-vm-required": "true"},
		},
		Spec: virtv1.VirtualMachineSpec{
			Running: pointer.Bool(false),
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
