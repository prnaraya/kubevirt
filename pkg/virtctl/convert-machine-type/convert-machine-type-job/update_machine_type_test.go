package convertmachinetypejob_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/pkg/testutils"
	. "kubevirt.io/kubevirt/pkg/virtctl/convert-machine-type/convert-machine-type-job"
)

const (
	unsupportedMachineType = "pc-q35-rhel8.2.0"
	aliasMachineType       = "q35"
	restartRequiredLabel   = "restart-vm-required"
)

var _ = Describe("Update Machine Type", func() {
	var ctrl *gomock.Controller
	var virtClient *kubecli.MockKubevirtClient
	var vmInterface *kubecli.MockVirtualMachineInterface
	var vmiInterface *kubecli.MockVirtualMachineInstanceInterface
	var kubeClient *fake.Clientset
	var vmiInformer cache.SharedIndexInformer
	var controller *JobController
	var exitJob chan struct{}
	var err error

	shouldExpectGetVMI := func(vmi *k6tv1.VirtualMachineInstance) {
		vmiInterface.EXPECT().Get(context.Background(), vmi.Name, &v1.GetOptions{}).Return(vmi, nil).Times(1)

		kubeClient.Fake.PrependReactor("get", "virtualmachineinstances", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			get, ok := action.(testing.GetAction)
			Expect(ok).To(BeTrue())
			Expect(get.GetNamespace()).To(Equal(v1.NamespaceDefault))
			Expect(get.GetName()).To(Equal(vmi.Name))
			return true, vmi, nil
		})
	}

	shouldExpectRestartRequiredLabel := func(vm *k6tv1.VirtualMachine) {
		patchData := `{"metadata":{"labels":{"restart-vm-required":""}}}`

		vmInterface.EXPECT().Patch(context.Background(), vm.Name, types.MergePatchType, []byte(patchData), &v1.PatchOptions{}).Times(1)

		kubeClient.Fake.PrependReactor("patch", "virtualmachines", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			patch, ok := action.(testing.PatchAction)
			Expect(ok).To(BeTrue())
			Expect(patch.GetPatch()).To(Equal([]byte(patchData)))
			Expect(patch.GetPatchType()).To(Equal(types.MergePatchType))
			Expect(vm.Labels).To(HaveKeyWithValue(restartRequiredLabel, "true"), "should apply `restart-vm-required` label to VM")
			return true, vm, nil
		})
	}

	shouldExpectPatchMachineType := func(vm *k6tv1.VirtualMachine) {
		patchData := fmt.Sprintf(`{"spec":{"template":{"spec":{"domain":{"machine":{"type":"pc-q35-%s"}}}}}}`, LatestMachineTypeVersion)

		vmInterface.EXPECT().Patch(context.Background(), vm.Name, types.MergePatchType, []byte(patchData), &v1.PatchOptions{}).Times(1)

		kubeClient.Fake.PrependReactor("patch", "virtualmachines", func(action testing.Action) (handled bool, obj runtime.Object, err error) {
			patch, ok := action.(testing.PatchAction)
			Expect(ok).To(BeTrue())
			Expect(patch.GetPatch()).To(Equal([]byte(patchData)))
			Expect(patch.GetPatchType()).To(Equal(types.MergePatchType))
			Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(fmt.Sprintf("pc-q35-%s", LatestMachineTypeVersion)))
			return true, vm, nil
		})
	}

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		virtClient = kubecli.NewMockKubevirtClient(ctrl)
		vmiInterface = kubecli.NewMockVirtualMachineInstanceInterface(ctrl)
		vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)
		kubeClient = fake.NewSimpleClientset()

		vmiInformer, _ = testutils.NewFakeInformerFor(&k6tv1.VirtualMachineInstance{})
		exitJob = make(chan struct{})
		controller, err = NewJobController(vmiInformer, virtClient, exitJob)
		Expect(err).ToNot(HaveOccurred())

		virtClient.EXPECT().VirtualMachine(v1.NamespaceDefault).Return(vmInterface).AnyTimes()
		virtClient.EXPECT().VirtualMachineInstance(v1.NamespaceDefault).Return(vmiInterface).AnyTimes()
		virtClient.EXPECT().VirtualMachine(v1.NamespaceAll).Return(vmInterface).AnyTimes()
		virtClient.EXPECT().VirtualMachineInstance(v1.NamespaceAll).Return(vmiInterface).AnyTimes()
		virtClient.EXPECT().CoreV1().Return(kubeClient.CoreV1()).AnyTimes()
	})

	Describe("UpdateMachineTypes", func() {
		Context("For VM with unsupported machine type", func() {

			It("should update VM machine type to latest version", func() {
				vm := newVMWithMachineType(unsupportedMachineType, false)

				vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{}).Return(&k6tv1.VirtualMachineList{
					Items: []k6tv1.VirtualMachine{*vm},
				}, nil).Times(1)

				shouldExpectPatchMachineType(vm)

				err := controller.UpdateMachineTypes()
				Expect(err).ToNot(HaveOccurred())
			})

			It("should restart VM when VM is running and restartNow=true", func() {
				vm := newVMWithMachineType(unsupportedMachineType, true)

				vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{}).Return(&k6tv1.VirtualMachineList{
					Items: []k6tv1.VirtualMachine{*vm},
				}, nil).Times(1)

				shouldExpectPatchMachineType(vm)
				vmi := newVMIWithMachineType(unsupportedMachineType, vm.Name)
				SetTestRestartNow(true)

				shouldExpectGetVMI(vmi)
				shouldExpectRestartRequiredLabel(vm)

				vmInterface.EXPECT().Restart(context.Background(), vm.Name, &k6tv1.RestartOptions{}).Times(1)

				err := controller.UpdateMachineTypes()
				Expect(err).ToNot(HaveOccurred())

				SetTestRestartNow(false)
			})

			Context("for multiple VMs", func() {

				It("should update machine types of all VMs", func() {
					vmDefaultNamespace := newUnsupportedVMWithNamespace(v1.NamespaceDefault, 1)
					vmKubevirtNamespace := newUnsupportedVMWithNamespace("kubevirt", 2)

					// "kubevirt" namespace will be used because no
					// namespace is specified
					virtClient.EXPECT().VirtualMachine("kubevirt").Return(vmInterface).Times(1)
					vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{}).Return(&k6tv1.VirtualMachineList{
						Items: []k6tv1.VirtualMachine{*vmDefaultNamespace, *vmKubevirtNamespace},
					}, nil).Times(1)

					shouldExpectPatchMachineType(vmDefaultNamespace)
					shouldExpectPatchMachineType(vmKubevirtNamespace)

					err := controller.UpdateMachineTypes()
					Expect(err).ToNot(HaveOccurred())
				})

				Context("for specified namespace", func() {
					// "kubevirt" will not be used, therefore don't
					// expect calls with "kubevirt" namespace
					It("should only update machine types of VMs in specified namespace", func() {
						vmDefaultNamespace := newUnsupportedVMWithNamespace(v1.NamespaceDefault, 1)
						vmKubevirtNamespace := newUnsupportedVMWithNamespace("kubevirt", 3)

						SetTestNamespace(v1.NamespaceDefault)

						vmList := []k6tv1.VirtualMachine{*vmDefaultNamespace, *vmKubevirtNamespace}

						vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{}).DoAndReturn(func(ctx context.Context, opts *v1.ListOptions) (*k6tv1.VirtualMachineList, error) {
							namespaceVMList := []k6tv1.VirtualMachine{}

							for _, vm := range vmList {
								if vm.Namespace == v1.NamespaceDefault {
									namespaceVMList = append(namespaceVMList, vm)
								}
							}

							return &k6tv1.VirtualMachineList{
								Items: namespaceVMList,
							}, nil
						}).Times(1)

						shouldExpectPatchMachineType(vmDefaultNamespace)

						err := controller.UpdateMachineTypes()
						Expect(err).ToNot(HaveOccurred())

						SetTestNamespace(v1.NamespaceAll)
					})
				})

				Context("for specified label-selector", func() {
					It("should only update machine types of VMs that satisfy label-selector conditions", func() {
						vmNoLabel := newUnsupportedVMWithLabel("", "", 1)
						vmWithLabel := newUnsupportedVMWithLabel("kubevirt.io/schedulable", "true", 2)
						vmWithWrongLabel := newUnsupportedVMWithLabel("kubevirt.io/schedulable", "false", 4)

						SetTestLabelSelector("kubevirt.io/schedulable=true")

						vmList := []k6tv1.VirtualMachine{*vmNoLabel, *vmWithLabel, *vmWithWrongLabel}
						listOpts := &v1.ListOptions{
							LabelSelector: "kubevirt.io/schedulable=true",
						}

						vmInterface.EXPECT().List(context.Background(), listOpts).DoAndReturn(func(ctx context.Context, opts *v1.ListOptions) (*k6tv1.VirtualMachineList, error) {
							labelVMList := []k6tv1.VirtualMachine{}

							for _, vm := range vmList {
								value, ok := vm.Labels["kubevirt.io/schedulable"]
								if ok && value == "true" {
									labelVMList = append(labelVMList, vm)
								}
							}

							return &k6tv1.VirtualMachineList{
								Items: labelVMList,
							}, nil
						}).Times(1)

						shouldExpectPatchMachineType(vmWithLabel)

						err := controller.UpdateMachineTypes()
						Expect(err).ToNot(HaveOccurred())

						SetTestLabelSelector("")
					})
				})
			})
		})

		Context("For VM with 'q35' alias machine type", func() {
			It("should restart VM when VM is running and restartNow=true", func() {
				vm := newVMWithMachineType(aliasMachineType, true)
				SetTestRestartNow(true)

				vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{}).Return(&k6tv1.VirtualMachineList{
					Items: []k6tv1.VirtualMachine{*vm},
				}, nil).Times(1)

				vmi := newVMIWithMachineType(aliasMachineType, vm.Name)

				shouldExpectGetVMI(vmi)
				shouldExpectRestartRequiredLabel(vm)

				vmInterface.EXPECT().Restart(context.Background(), vm.Name, &k6tv1.RestartOptions{}).Times(1)

				err := controller.UpdateMachineTypes()
				Expect(err).ToNot(HaveOccurred())

				SetTestRestartNow(false)
			})
		})

		Context("For running VM with supported machine type", func() {
			// Ensure there are no unexpected calls to patch VM,
			// list VMI, or restart VM
			It("Should not update VM machine type", func() {
				vm := newVMWithMachineType(fmt.Sprintf("pc-q35-%s", LatestMachineTypeVersion), true)

				vmInterface.EXPECT().List(context.Background(), &v1.ListOptions{}).Return(&k6tv1.VirtualMachineList{
					Items: []k6tv1.VirtualMachine{*vm},
				}, nil).Times(1)

				err := controller.UpdateMachineTypes()
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})

	Describe("IsMachineTypeUpdated", func() {

		DescribeTable("when machine type is", func(machineType string) {
			needsUpdate, updatedMachineType, err := IsMachineTypeUpdated(machineType)
			Expect(err).ToNot(HaveOccurred())
			parsedMachineType := parseMachineType(machineType)
			updateMachineTypeVersion := fmt.Sprintf("pc-q35-%s", LatestMachineTypeVersion)

			if parsedMachineType >= MinimumSupportedMachineTypeVersion {
				Expect(updatedMachineType).To(Equal(machineType))
				Expect(needsUpdate).To(BeFalse())
			} else if machineType == "q35" {
				Expect(updatedMachineType).To(Equal(machineType))
				Expect(needsUpdate).To(BeTrue())
			} else {
				Expect(updatedMachineType).To(Equal(updateMachineTypeVersion))
				Expect(needsUpdate).To(BeTrue())
			}
		},
			Entry("'q35' should mark VM as needing update", "q35"),
			Entry("unsupported should mark VM as needing update", "pc-q35-rhel8.2.0"),
			Entry("supported should not mark VM as needing update", "pc-q35-rhel9.2.0"),
		)

		Context("with invalid machine type input", func() {
			It("should return an error", func() {
				invalidMachineType := "pc-rhel9"
				needsUpdate, updatedMachineType, err := IsMachineTypeUpdated(invalidMachineType)
				Expect(needsUpdate).To(BeFalse())
				Expect(updatedMachineType).To(Equal(invalidMachineType))
				Expect(err).To(MatchError(fmt.Errorf("invalid machine type: %s", invalidMachineType)))
			})
		})
	})
})

func newVMWithMachineType(machineType string, running bool) *k6tv1.VirtualMachine {
	vmName := "test-vm-" + machineType
	testVM := &k6tv1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{
			Name:      vmName,
			Namespace: v1.NamespaceDefault,
		},
		Spec: k6tv1.VirtualMachineSpec{
			Running: &running,
			Template: &k6tv1.VirtualMachineInstanceTemplateSpec{
				Spec: k6tv1.VirtualMachineInstanceSpec{
					Domain: k6tv1.DomainSpec{
						Machine: &k6tv1.Machine{
							Type: machineType,
						},
					},
				},
			},
		},
	}
	testVM.Labels = map[string]string{}
	return testVM
}

func newVMIWithMachineType(machineType string, name string) *k6tv1.VirtualMachineInstance {
	statusMachineType := machineType
	if machineType == "q35" {
		statusMachineType = "pc-q35-rhel8.2.0"
	}

	testvmi := &k6tv1.VirtualMachineInstance{
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: v1.NamespaceDefault,
		},
		Spec: k6tv1.VirtualMachineInstanceSpec{
			Domain: k6tv1.DomainSpec{
				Machine: &k6tv1.Machine{
					Type: machineType,
				},
			},
		},
		Status: k6tv1.VirtualMachineInstanceStatus{
			Machine: &k6tv1.Machine{
				Type: statusMachineType,
			},
		},
	}

	return testvmi
}

func newUnsupportedVMWithNamespace(namespace string, count int) *k6tv1.VirtualMachine {
	vmName := fmt.Sprintf("test-vm%d-%s", count, unsupportedMachineType)
	testVM := &k6tv1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{
			Name:      vmName,
			Namespace: namespace,
		},
		Spec: k6tv1.VirtualMachineSpec{
			Running: pointer.Bool(false),
			Template: &k6tv1.VirtualMachineInstanceTemplateSpec{
				Spec: k6tv1.VirtualMachineInstanceSpec{
					Domain: k6tv1.DomainSpec{
						Machine: &k6tv1.Machine{
							Type: unsupportedMachineType,
						},
					},
				},
			},
		},
	}
	return testVM
}

func newUnsupportedVMWithLabel(labelKey, labelValue string, count int) *k6tv1.VirtualMachine {
	vmName := fmt.Sprintf("test-vm%d-%s", count, unsupportedMachineType)
	testVM := &k6tv1.VirtualMachine{
		ObjectMeta: v1.ObjectMeta{
			Name:      vmName,
			Namespace: v1.NamespaceDefault,
		},
		Spec: k6tv1.VirtualMachineSpec{
			Running: pointer.Bool(false),
			Template: &k6tv1.VirtualMachineInstanceTemplateSpec{
				Spec: k6tv1.VirtualMachineInstanceSpec{
					Domain: k6tv1.DomainSpec{
						Machine: &k6tv1.Machine{
							Type: unsupportedMachineType,
						},
					},
				},
			},
		},
	}
	testVM.Labels = map[string]string{}
	if labelKey != "" {
		testVM.Labels[labelKey] = labelValue
	}
	return testVM
}

func parseMachineType(machineType string) string {
	parsedMachineType := "q35"
	if machineType != "q35" {
		splitMachineType := strings.Split(machineType, "-")
		parsedMachineType = splitMachineType[2]
	}
	return parsedMachineType
}
