package convertmachinetype_test

import (
	"context"

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
	var vm *virtv1.VirtualMachine
	var vmi *virtv1.VirtualMachineInstance
	var err error

	initController := func() {
		controller, err = NewJobController(vmiInformer, virtClient)
		Expect(err).ToNot(HaveOccurred())

		mockQueue = testutils.NewMockWorkQueue(controller.Queue)
		controller.Queue = mockQueue
		vmiFeeder = testutils.NewVirtualMachineFeeder(mockQueue, vmiSource)
	}

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

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			virtClient = kubecli.NewMockKubevirtClient(ctrl)
			vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)
			vmiInterface = kubecli.NewMockVirtualMachineInstanceInterface(ctrl)

			vmiInformer, vmiSource = testutils.NewFakeInformerFor(&virtv1.VirtualMachineInstance{})
			initController()
			kubeClient = fake.NewSimpleClientset()

			virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()
			virtClient.EXPECT().VirtualMachineInstance(gomock.Any()).Return(vmiInterface).AnyTimes()

			vm = newVMWithRestartLabel()
			vmi = newVMIWithMachineType(machineTypeNoUpdate, vm.Name)

			shouldExpectGetVM(vm)
			shouldExpectVMICreation(vmi)
			vmInterface.EXPECT().List(context.Background(), &metav1.ListOptions{
				LabelSelector: `restart-vm-required=""`,
			}).Return(&virtv1.VirtualMachineList{
				Items: []virtv1.VirtualMachine{},
			}, nil).Times(1)

			ExitJob = make(chan struct{})
			go vmiInformer.Run(ExitJob)
			Expect(cache.WaitForCacheSync(ExitJob, vmiInformer.HasSynced)).To(BeTrue())
			vmiFeeder.Add(vmi)
		})

		AfterEach(func() {
			close(ExitJob)
		})

		Context("if VM machine type has been updated", func() {
			It("should remove `restart-vm-required` label from VM", func() {
				shouldExpectVMIDeletion(vmi)
				shouldExpectRemoveLabel(vm)

				vmiFeeder.Delete(vmi)
				controller.Execute()
				Expect(ExitJob).To(BeClosed(), "should signal job termination when no VMs with 'restart-vm-required' label remain")
			})
		})

		Context("if VM machine type has not been updated", func() {
			It("should not remove `restart-vm-required` label from VM", func() {
				shouldExpectVMIDeletion(vmi)

				vmiFeeder.Delete(vmi)
				controller.Execute()

				Expect(vm.Labels).To(HaveKey("restart-vm-required"))
			})
		})
	})
})

func newVMWithRestartLabel() *virtv1.VirtualMachine {
	testVM := &virtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-vm",
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
	return testVM
}
