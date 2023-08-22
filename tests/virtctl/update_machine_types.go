package virtctl

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/clientcmd"
	"kubevirt.io/kubevirt/tests/util"
)

const (
	latestMachineType      = "pc-q35-rhel9.2.0"
	unsupportedMachineType = "pc-q35-rhel7.0.0"
	supportedMachineType   = "pc-q35-rhel9.0.0"
	update                 = "update"
	machineTypes           = "machine-types"
	restartRequiredLabel   = "restart-vm-required"
	namespaceFlag          = "namespace"
	labelSelectorFlag      = "label-selector"
	forceRestartFlag       = "force-restart"
)

var _ = Describe("[sig-compute][virtctl] mass machine type transition", func() {
	var virtClient kubecli.KubevirtClient
	var err error

	BeforeEach(func() {
		virtClient, err = kubecli.GetKubevirtClient()
		Expect(err).ToNot(HaveOccurred())
	})

	expectJobExists := func() *batchv1.Job {
		jobs, err := virtClient.BatchV1().Jobs(metav1.NamespaceDefault).List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		job, ok := hasJob(jobs)
		Expect(ok).To(BeTrue())
		return job
	}

	deleteJob := func(job *batchv1.Job) {
		err = virtClient.BatchV1().Jobs(metav1.NamespaceDefault).Delete(context.Background(), job.Name, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
	}

	Describe("should update the machine types of outdated VMs", func() {

		It("no arguments are passed to virtctl command", func() {
			vmUnsupportedStopped := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, false)
			vmUnsupportedRunning := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, true)
			vmSupported := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, false)

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes)()
			Expect(err).ToNot(HaveOccurred())

			job := expectJobExists()

			Expect(vmUnsupportedStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineType))
			Expect(vmUnsupportedRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineType))
			Expect(vmSupported.Spec.Template.Spec.Domain.Machine.Type).To(Equal(supportedMachineType))

			Expect(vmUnsupportedRunning.Labels).To(HaveKey(restartRequiredLabel))

			deleteJob(job)
		})

		It("Example with namespace flag", func() {
			vmNamespaceDefaultStopped := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, false)
			vmNamespaceDefaultRunning := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, true)
			vmNamespaceOtherStopped := createVM(virtClient, unsupportedMachineType, metav1.NamespaceDefault, false, false)
			vmNamespaceOtherRunning := createVM(virtClient, unsupportedMachineType, metav1.NamespaceDefault, false, true)

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes, setFlag(namespaceFlag, metav1.NamespaceDefault))()
			Expect(err).ToNot(HaveOccurred())

			job := expectJobExists()

			Expect(vmNamespaceDefaultStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(unsupportedMachineType))
			Expect(vmNamespaceDefaultRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(unsupportedMachineType))
			Expect(vmNamespaceOtherStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineType))
			Expect(vmNamespaceOtherRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineType))

			Expect(vmNamespaceDefaultRunning.Labels).ToNot(HaveKey(restartRequiredLabel))
			Expect(vmNamespaceOtherRunning.Labels).To(HaveKey(restartRequiredLabel))

			deleteJob(job)
		})

		It("Example with label-selector flag", func() {
			vmWithLabelStopped := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, true, false)
			vmWithLabelRunning := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, true, true)
			vmNoLabelStopped := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, false)
			vmNoLabelRunning := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, true)

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes, setFlag(labelSelectorFlag, "testing-label=true"))()
			Expect(err).ToNot(HaveOccurred())

			job := expectJobExists()

			Expect(vmWithLabelStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineType))
			Expect(vmWithLabelRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(latestMachineType))
			Expect(vmNoLabelStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(unsupportedMachineType))
			Expect(vmNoLabelRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(unsupportedMachineType))

			Expect(vmWithLabelRunning.Labels).To(HaveKey(restartRequiredLabel))
			Expect(vmNoLabelRunning.Labels).ToNot(HaveKey(restartRequiredLabel))

			deleteJob(job)
		})

		// It("Example with force-restart flag", func() {
		// 	vmUnsupportedStopped := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, false)
		// 	vmUnsupportedRunning := createVM(virtClient, unsupportedMachineType, util.NamespaceTestDefault, false, true)

		// 	job := expectJobExists()

		// 	deleteJob(job)
		// })

		// It("Complex example", func() {

		// })
	})
})

func createVM(virtClient kubecli.KubevirtClient, machineType, namespace string, running, hasLabel bool) *v1.VirtualMachine {
	template := tests.NewRandomVMI()
	template.ObjectMeta.Namespace = namespace
	template.Spec.Domain.Machine = &v1.Machine{Type: machineType}
	if hasLabel {
		template.ObjectMeta.Labels = map[string]string{"testing-label": "true"}
	}

	vm := tests.NewRandomVirtualMachine(template, false)

	vm, err := virtClient.VirtualMachine(namespace).Create(context.Background(), vm)
	Expect(err).ToNot(HaveOccurred())

	if !running {
		vm = tests.StopVirtualMachine(vm)
	}
	return vm
}

func hasJob(jobs *batchv1.JobList) (*batchv1.Job, bool) {
	for _, job := range jobs.Items {
		if strings.Contains(job.Name, "convert-machine-type") {
			return &job, true
		}
	}

	return nil, false
}
