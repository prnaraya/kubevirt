package massmachinetypetransition_test

import (
	"context"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k6tv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	mmtt "kubevirt.io/kubevirt/pkg/virtctl/massmachinetypetransition"
)

var _ = Describe("Informers", func() {

	Describe("removeWarningLabel", func() {

		It("should remove 'restart-vm-required' label from VM", func() {
			ctrl := gomock.NewController(GinkgoT())
			virtClient := kubecli.NewMockKubevirtClient(ctrl)
			vmInterface := kubecli.NewMockVirtualMachineInterface(ctrl)
			virtClient.EXPECT().VirtualMachine(gomock.Any()).Return(vmInterface).AnyTimes()

			testVM := newVmWithLabel()

			vmInterface.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(ctx context.Context, vmName string, pt types.PatchType, data []byte, patchOpts *k8sv1.PatchOptions, subresources ...string) {
				delete(testVM.Labels, "restart-vm-required")
			}).AnyTimes()

			err := mmtt.RemoveWarningLabel(virtClient, testVM)
			Expect(err).ToNot(HaveOccurred())
			Expect(testVM.Labels).ToNot(HaveKeyWithValue("restart-vm-required", "true"))
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
