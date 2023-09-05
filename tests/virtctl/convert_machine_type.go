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

	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"

	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/clientcmd"
	"kubevirt.io/kubevirt/tests/decorators"
	"kubevirt.io/kubevirt/tests/util"
)

const (
	machineTypeNeedsUpdate = "pc-q35-rhel8.2.0"
	machineTypeNoUpdate    = "pc-q35-rhel9.0.0"
	machineTypeGlob        = "*rhel8.*"
	update                 = "update"
	machineTypes           = "machine-types"
	restartRequiredLabel   = "restart-vm-required"
	machineTypeFlag        = "which-matches-glob"
	namespaceFlag          = "namespace"
	labelSelectorFlag      = "label-selector"
	forceRestartFlag       = "force-restart"
	testLabel              = "testing-label=true"
)

var _ = Describe("[sig-compute][virtctl] mass machine type transition", decorators.SigCompute, func() {
	var virtClient kubecli.KubevirtClient
	var err error

	BeforeEach(func() {
		virtClient, err = kubecli.GetKubevirtClient()
		Expect(err).ToNot(HaveOccurred())
	})

	expectJobExists := func() *batchv1.Job {
		var job *batchv1.Job
		var ok bool

		Eventually(func() bool {
			jobs, err := virtClient.BatchV1().Jobs(metav1.NamespaceDefault).List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			job, ok = hasJob(jobs)
			return ok
		}).Should(BeTrue())

		return job
	}

	deleteJob := func(job *batchv1.Job) {
		err = virtClient.BatchV1().Jobs(metav1.NamespaceDefault).Delete(context.Background(), job.Name, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
	}

	Describe("should update the machine types of outdated VMs", func() {

		It("no arguments are passed to virtctl command", func() {
			vmNeedsUpdateStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)
			vmNeedsUpdateRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, true)
			vmNoUpdate := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes, setFlag(machineTypeFlag, machineTypeGlob))()
			Expect(err).ToNot(HaveOccurred())

			job := expectJobExists()

			Expect(vmNeedsUpdateStopped.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNeedsUpdateRunning.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNoUpdate.Spec.Template.Spec.Domain.Machine).To(Equal(machineTypeNoUpdate))

			Expect(vmNeedsUpdateRunning.Labels).To(HaveKey(restartRequiredLabel))

			deleteJob(job)
		})

		It("Example with namespace flag", func() {
			vmNamespaceDefaultStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)
			vmNamespaceDefaultRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, true)
			vmNamespaceOtherStopped := createVM(virtClient, machineTypeNeedsUpdate, metav1.NamespaceDefault, false, false)
			vmNamespaceOtherRunning := createVM(virtClient, machineTypeNeedsUpdate, metav1.NamespaceDefault, false, true)

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes,
				setFlag(machineTypeFlag, machineTypeGlob),
				setFlag(namespaceFlag, metav1.NamespaceDefault))()
			Expect(err).ToNot(HaveOccurred())

			job := expectJobExists()

			Expect(vmNamespaceDefaultStopped.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNamespaceDefaultRunning.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNamespaceOtherStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNamespaceOtherRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))

			Expect(vmNamespaceDefaultRunning.Labels).ToNot(HaveKey(restartRequiredLabel))
			Expect(vmNamespaceOtherRunning.Labels).To(HaveKey(restartRequiredLabel))

			deleteJob(job)
		})

		It("Example with label-selector flag", func() {
			vmWithLabelStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, true, false)
			vmWithLabelRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, true, true)
			vmNoLabelStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)
			vmNoLabelRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, true)

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes,
				setFlag(machineTypeFlag, machineTypeGlob),
				setFlag(labelSelectorFlag, testLabel))()
			Expect(err).ToNot(HaveOccurred())

			job := expectJobExists()

			Expect(vmWithLabelStopped.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmWithLabelRunning.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNoLabelStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNoLabelRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))

			Expect(vmWithLabelRunning.Labels).To(HaveKey(restartRequiredLabel))
			Expect(vmNoLabelRunning.Labels).ToNot(HaveKey(restartRequiredLabel))

			deleteJob(job)
		})

		It("Example with force-restart flag", func() {
			vmNeedsUpdateStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)
			vmNeedsUpdateRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, true)

			job := expectJobExists()

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes,
				setFlag(machineTypeFlag, machineTypeGlob),
				setFlag(forceRestartFlag, "true"))()
			Expect(err).ToNot(HaveOccurred())

			Expect(vmNeedsUpdateStopped.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNeedsUpdateRunning.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNeedsUpdateRunning.Labels).ToNot(HaveKey(restartRequiredLabel))

			// wait for vm to restart and check machine type in VMI spec
			Eventually(func() bool {
				vm, err := virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vmNeedsUpdateRunning.Name, &metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return *vm.Spec.Running
			}).Should(BeTrue())

			vmi, err := virtClient.VirtualMachineInstance(util.NamespaceTestDefault).Get(context.Background(), vmNeedsUpdateRunning.Name, &metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vmNeedsUpdateRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(virtconfig.DefaultAMD64MachineType))
			Expect(vmi.Spec.Domain.Machine.Type).To(Equal(virtconfig.DefaultAMD64MachineType))

			deleteJob(job)
		})

		It("Complex example", func() {
			vmNamespaceDefaultStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)
			vmNamespaceDefaultRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, true)
			vmNamespaceOtherStopped := createVM(virtClient, machineTypeNeedsUpdate, metav1.NamespaceDefault, false, false)
			vmNamespaceOtherRunning := createVM(virtClient, machineTypeNeedsUpdate, metav1.NamespaceDefault, false, true)
			vmNamespaceDefaultWithLabelStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, true, false)
			vmNamespaceDefaultWithLabelRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, true, true)
			vmNamespaceOtherWithLabelStopped := createVM(virtClient, machineTypeNeedsUpdate, metav1.NamespaceDefault, true, false)
			vmNamespaceOtherWithLabelRunning := createVM(virtClient, machineTypeNeedsUpdate, metav1.NamespaceDefault, true, true)
			vmNoUpdate := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)

			job := expectJobExists()

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes,
				setFlag(machineTypeFlag, machineTypeGlob),
				setFlag(namespaceFlag, util.NamespaceTestDefault),
				setFlag(labelSelectorFlag, testLabel),
				setFlag(forceRestartFlag, "true"))()
			Expect(err).ToNot(HaveOccurred())

			// the only VMs that should be updated are the ones in the default test namespace and with the test label
			// with force-restart flag, the restart-vm-required label should not be applied to the running VM
			Expect(vmNamespaceDefaultWithLabelStopped.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNamespaceDefaultWithLabelRunning.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNamespaceDefaultWithLabelRunning.Labels).ToNot(HaveKey(restartRequiredLabel))

			// all other VM machine types should remain unchanged
			Expect(vmNamespaceDefaultStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNamespaceDefaultRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNamespaceOtherStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNamespaceOtherRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNamespaceOtherWithLabelStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNamespaceOtherWithLabelRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmNoUpdate.Spec.Template.Spec.Domain.Machine).To(Equal(machineTypeNoUpdate))

			Eventually(func() bool {
				vm, err := virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vmNamespaceDefaultWithLabelRunning.Name, &metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return *vm.Spec.Running
			}).Should(BeTrue())

			vmi, err := virtClient.VirtualMachineInstance(util.NamespaceTestDefault).Get(context.Background(), vmNamespaceDefaultWithLabelRunning.Name, &metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vmNamespaceDefaultWithLabelRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(virtconfig.DefaultAMD64MachineType))
			Expect(vmi.Spec.Domain.Machine.Type).To(Equal(virtconfig.DefaultAMD64MachineType))

			deleteJob(job)
		})
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
