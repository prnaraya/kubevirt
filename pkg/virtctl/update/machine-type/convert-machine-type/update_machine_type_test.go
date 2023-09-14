package convertmachinetype_test

import (
	"context"
	"fmt"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/pointer"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/pkg/testutils"
	. "kubevirt.io/kubevirt/pkg/virtctl/update/machine-type/convert-machine-type"
)

const (
	machineTypeGlob              = "*rhel8.*"
	machineTypeNeedsUpdate       = "pc-q35-rhel8.2.0"
	machineTypeNoUpdate          = "pc-q35-rhel9.0.0"
	restartRequiredLabel         = "restart-vm-required"
	restartRequiredLabelSelector = "restart-vm-required="
	testLabelSelector            = "kubevirt.io/schedulable=true"
)

var _ = Describe("Update Machine Type", func() {
	var ctrl *gomock.Controller
	var virtClient *kubecli.MockKubevirtClient
	var vmInterface *kubecli.MockVirtualMachineInterface
	var vmiInterface *kubecli.MockVirtualMachineInstanceInterface
	var vmiInformer cache.SharedIndexInformer
	var controller *JobController
	var err error

	shouldExpectGetVMI := func(vmi *k6tv1.VirtualMachineInstance) {
		vmiInterface.EXPECT().Get(context.Background(), vmi.Name, &v1.GetOptions{}).Return(vmi, nil).Times(1)
	}

	shouldExpectRestartRequiredLabel := func(vm *k6tv1.VirtualMachine) {
		patchData := `{"metadata":{"labels":{"restart-vm-required":""}}}`

		vmInterface.EXPECT().Patch(context.Background(), vm.Name, types.MergePatchType, []byte(patchData), &v1.PatchOptions{}).DoAndReturn(func(ctx context.Context, vmName string, patchType types.PatchType, patch []byte, opts *v1.PatchOptions, subresources ...interface{}) (*k6tv1.VirtualMachine, error) {
			vm.Labels["restart-vm-required"] = ""
			return vm, nil
		}).Times(1)
	}

	shouldExpectPatchMachineType := func(vm *k6tv1.VirtualMachine) {
		patchData := `[{"op": "remove", "path": "/spec/template/spec/domain/machine"}]`

		vmInterface.EXPECT().Patch(context.Background(), vm.Name, types.JSONPatchType, []byte(patchData), &v1.PatchOptions{}).DoAndReturn(func(ctx context.Context, vmName string, patchType types.PatchType, patch []byte, opts *v1.PatchOptions, subresources ...interface{}) (*k6tv1.VirtualMachine, error) {
			vm.Spec.Template.Spec.Domain.Machine = nil
			return vm, nil
		}).Times(1)
	}

	shouldExpectList := func(vmList []k6tv1.VirtualMachine) {
		vmInterface.EXPECT().List(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, opts *v1.ListOptions) (*k6tv1.VirtualMachineList, error) {
			vmListNamespace := []k6tv1.VirtualMachine{}
			vmListLabel := []k6tv1.VirtualMachine{}
			vmListRestartRequired := []k6tv1.VirtualMachine{}

			if opts.LabelSelector == "" {
				for _, vm := range vmList {
					if Namespace == v1.NamespaceAll {
						return &k6tv1.VirtualMachineList{
							Items: vmList,
						}, nil

					} else if vm.Namespace == Namespace {
						vmListNamespace = append(vmListLabel, vm)
					}
				}
				return &k6tv1.VirtualMachineList{
					Items: vmListNamespace,
				}, nil
			}

			if opts.LabelSelector == testLabelSelector {
				for _, vm := range vmList {
					value, ok := vm.Labels["kubevirt.io/schedulable"]
					if ok && value == "true" {
						vmListLabel = append(vmListLabel, vm)
					}
				}

				return &k6tv1.VirtualMachineList{
					Items: vmListLabel,
				}, nil
			}

			if opts.LabelSelector == restartRequiredLabelSelector {
				for _, vm := range vmList {
					value, ok := vm.Labels["restart-vm-required"]
					if ok && value == "" {
						vmListRestartRequired = append(vmListRestartRequired, vm)
					}
					fmt.Printf("Num vmis pending update: %d", len(vmListRestartRequired))
				}

				return &k6tv1.VirtualMachineList{
					Items: vmListRestartRequired,
				}, nil
			}

			return &k6tv1.VirtualMachineList{}, nil
		}).AnyTimes()
	}

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		virtClient = kubecli.NewMockKubevirtClient(ctrl)
		vmiInterface = kubecli.NewMockVirtualMachineInstanceInterface(ctrl)
		vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)

		vmiInformer, _ = testutils.NewFakeInformerFor(&k6tv1.VirtualMachineInstance{})
		controller, err = NewJobController(vmiInformer, virtClient)
		Expect(err).ToNot(HaveOccurred())

		virtClient.EXPECT().VirtualMachine(v1.NamespaceDefault).Return(vmInterface).AnyTimes()
		virtClient.EXPECT().VirtualMachineInstance(v1.NamespaceDefault).Return(vmiInterface).AnyTimes()
		virtClient.EXPECT().VirtualMachine(v1.NamespaceAll).Return(vmInterface).AnyTimes()
		virtClient.EXPECT().VirtualMachineInstance(v1.NamespaceAll).Return(vmiInterface).AnyTimes()

		MachineTypeGlob = machineTypeGlob
	})

	Context("For VM with machine type matching machine type glob", func() {

		It("should remove machine type from VM spec", func() {
			vm := newVMWithMachineType(machineTypeNeedsUpdate, false)
			vmList := []k6tv1.VirtualMachine{*vm}

			shouldExpectList(vmList)
			shouldExpectPatchMachineType(vm)

			err := controller.UpdateMachineTypes()
			Expect(err).ToNot(HaveOccurred())

			Expect(vm.Spec.Template.Spec.Domain.Machine).To(BeNil())
		})

		It("should apply 'restart-vm-required' label when VM is running", func() {
			vm := newVMWithMachineType(machineTypeNeedsUpdate, true)
			vmList := []k6tv1.VirtualMachine{*vm}

			shouldExpectList(vmList)
			shouldExpectPatchMachineType(vm)
			vmi := newVMIWithMachineType(machineTypeNeedsUpdate, vm.Name)

			shouldExpectGetVMI(vmi)
			shouldExpectRestartRequiredLabel(vm)

			err := controller.UpdateMachineTypes()
			Expect(err).ToNot(HaveOccurred())

			Expect(vm.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vm.Labels).To(HaveKey(restartRequiredLabel))
		})

		It("should restart VM when VM is running and restartNow=true", func() {
			vm := newVMWithMachineType(machineTypeNeedsUpdate, true)
			vmList := []k6tv1.VirtualMachine{*vm}

			shouldExpectList(vmList)
			shouldExpectPatchMachineType(vm)
			vmi := newVMIWithMachineType(machineTypeNeedsUpdate, vm.Name)
			RestartNow = true

			shouldExpectGetVMI(vmi)

			vmInterface.EXPECT().Restart(context.Background(), vm.Name, &k6tv1.RestartOptions{}).Times(1)

			err := controller.UpdateMachineTypes()
			Expect(err).ToNot(HaveOccurred())

			Expect(vm.Spec.Template.Spec.Domain.Machine).To(BeNil())

			RestartNow = false
		})

		Context("for multiple VMs", func() {

			It("should update machine types of all VMs with specified machine type", func() {
				vmDefaultNamespace := newVMWithNamespace(v1.NamespaceDefault, 1)
				vmKubevirtNamespace := newVMWithNamespace("kubevirt", 2)
				vmList := []k6tv1.VirtualMachine{*vmDefaultNamespace, *vmKubevirtNamespace}

				// "kubevirt" namespace will be used because no
				// namespace is specified
				virtClient.EXPECT().VirtualMachine("kubevirt").Return(vmInterface).Times(1)

				shouldExpectList(vmList)
				shouldExpectPatchMachineType(vmDefaultNamespace)
				shouldExpectPatchMachineType(vmKubevirtNamespace)

				err := controller.UpdateMachineTypes()
				Expect(err).ToNot(HaveOccurred())

				Expect(vmDefaultNamespace.Spec.Template.Spec.Domain.Machine).To(BeNil())
				Expect(vmKubevirtNamespace.Spec.Template.Spec.Domain.Machine).To(BeNil())
			})

			Context("for specified namespace", func() {
				// "kubevirt" will not be used, therefore don't
				// expect calls with "kubevirt" namespace
				It("should only update machine types of VMs with specified machine type in specified namespace", func() {
					vmDefaultNamespace := newVMWithNamespace(v1.NamespaceDefault, 1)
					vmKubevirtNamespace := newVMWithNamespace("kubevirt", 3)

					Namespace = v1.NamespaceDefault

					vmList := []k6tv1.VirtualMachine{*vmDefaultNamespace, *vmKubevirtNamespace}

					shouldExpectList(vmList)
					shouldExpectPatchMachineType(vmDefaultNamespace)

					err := controller.UpdateMachineTypes()
					Expect(err).ToNot(HaveOccurred())

					Expect(vmDefaultNamespace.Spec.Template.Spec.Domain.Machine).To(BeNil())
					Expect(vmKubevirtNamespace.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))

					Namespace = v1.NamespaceAll
				})
			})

			Context("for specified label-selector", func() {
				It("should only update machine types of VMs with specified machine type that satisfy label-selector conditions", func() {
					vmNoLabel := newVMWithLabel("", "", 1)
					vmWithLabel := newVMWithLabel("kubevirt.io/schedulable", "true", 2)
					vmWithWrongLabel := newVMWithLabel("kubevirt.io/schedulable", "false", 4)

					LabelSelector = testLabelSelector

					vmList := []k6tv1.VirtualMachine{*vmNoLabel, *vmWithLabel, *vmWithWrongLabel}

					shouldExpectList(vmList)
					shouldExpectPatchMachineType(vmWithLabel)

					err = controller.UpdateMachineTypes()
					Expect(err).ToNot(HaveOccurred())

					Expect(vmWithLabel.Spec.Template.Spec.Domain.Machine).To(BeNil())
					Expect(vmNoLabel.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
					Expect(vmWithWrongLabel.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))

					LabelSelector = ""
				})
			})
		})
	})

	Context("For running VM with machine type not matching machine type glob", func() {
		// Ensure there are no unexpected calls to patch VM,
		// list VMI, or restart VM
		It("Should not update VM machine type", func() {
			vm := newVMWithMachineType(machineTypeNoUpdate, true)
			vmList := []k6tv1.VirtualMachine{*vm}

			vmi := newVMIWithMachineType(machineTypeNoUpdate, vm.Name)
			RestartNow = true

			shouldExpectList(vmList)
			shouldExpectGetVMI(vmi)

			err := controller.UpdateMachineTypes()
			Expect(err).ToNot(HaveOccurred())

			Expect(vm.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNoUpdate))
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
			Phase: k6tv1.Running,
		},
	}

	return testvmi
}

func newVMWithNamespace(namespace string, count int) *k6tv1.VirtualMachine {
	vmName := fmt.Sprintf("test-vm%d-%s", count, machineTypeNeedsUpdate)
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
							Type: machineTypeNeedsUpdate,
						},
					},
				},
			},
		},
	}
	return testVM
}

func newVMWithLabel(labelKey, labelValue string, count int) *k6tv1.VirtualMachine {
	vmName := fmt.Sprintf("test-vm%d-%s", count, machineTypeNeedsUpdate)
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
							Type: machineTypeNeedsUpdate,
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
