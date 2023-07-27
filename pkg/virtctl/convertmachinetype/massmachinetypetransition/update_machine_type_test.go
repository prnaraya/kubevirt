package massmachinetypetransition_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	mmtt "kubevirt.io/kubevirt/pkg/virtctl/massmachinetypetransition"
)

var _ = Describe("Update Machine Type", func() {
	var ctrl *gomock.Controller
	var virtClient *kubecli.MockKubevirtClient
	var vmInterface *kubecli.MockVirtualMachineInterface
	var vmiInterface *kubecli.MockVirtualMachineInstanceInterface

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		virtClient = kubecli.NewMockKubevirtClient(ctrl)
		vmiInterface = kubecli.NewMockVirtualMachineInstanceInterface(ctrl)
		vmInterface = kubecli.NewMockVirtualMachineInterface(ctrl)
		virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()
		virtClient.EXPECT().VirtualMachineInstance(gomock.Any()).Return(vmiInterface).AnyTimes()
	})

	Describe("addWarningLabel", func() {

		It("should add VM Key to list of VMIs that need to be restarted", func() {
			vm := newVMWithMachineType("q35", true)
			vm.Labels = map[string]string{}

			vmInterface.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx context.Context, vmName string, pt types.PatchType, data []byte, patchOpts *k8sv1.PatchOptions, subresources ...string) {
				vm.Labels["restart-vm-required"] = "true"
			}).AnyTimes()

			err := mmtt.AddWarningLabel(virtClient, vm)
			Expect(err).ToNot(HaveOccurred())
			vmKey, err := cache.MetaNamespaceKeyFunc(vm)
			Expect(err).ToNot(HaveOccurred())
			Expect(mmtt.VmisPendingUpdate).To(HaveKey(vmKey))

			Expect(vm.Labels).To(HaveKeyWithValue("restart-vm-required", "true"), "VM should have 'restart-vm-required' label")

			delete(mmtt.VmisPendingUpdate, vmKey)
		})
	})

	Describe("verifyMachineType", func() {

		DescribeTable("when machine type is", func(machineType string) {
			needsUpdate, updatedMachineType := mmtt.VerifyMachineType(machineType)
			parsedMachineType := parseMachineType(machineType)
			updateMachineTypeVersion := fmt.Sprintf("pc-q35-%s", mmtt.LatestMachineTypeVersion)

			if parsedMachineType >= mmtt.MinimumSupportedMachineTypeVersion {
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
			Entry("'pc-q35-rhelx.x.x' and less than minimum supported machine type version should mark VM as needing update", "pc-q35-rhel8.2.0"),
			Entry("'pc-q35-rhelx.x.x' and greater than or equal to latest machine type version should not mark VM as needing update", "pc-q35-rhel9.0.0"),
		)
	})
})

func newVMWithMachineType(machineType string, running bool) *k6tv1.VirtualMachine {
	vmName := "test-vm-" + machineType
	testVM := &k6tv1.VirtualMachine{
		ObjectMeta: k8sv1.ObjectMeta{
			Name:      vmName,
			Namespace: k8sv1.NamespaceDefault,
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

func parseMachineType(machineType string) string {
	parsedMachineType := "q35"
	if machineType != "q35" {
		splitMachineType := strings.Split(machineType, "-")
		parsedMachineType = splitMachineType[2]
	}
	return parsedMachineType
}
