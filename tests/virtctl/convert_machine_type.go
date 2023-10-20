package virtctl

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gcustom"
	"github.com/onsi/gomega/gstruct"
	"github.com/onsi/gomega/types"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/kubecli"

	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"

	"kubevirt.io/kubevirt/tests"
	"kubevirt.io/kubevirt/tests/clientcmd"
	"kubevirt.io/kubevirt/tests/decorators"
	. "kubevirt.io/kubevirt/tests/framework/matcher"
	"kubevirt.io/kubevirt/tests/util"
)

const (
	machineTypeNeedsUpdate = "pc-q35-rhel8.2.0"
	machineTypeNoUpdate    = "pc-q35-9.0.0"
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

var _ = FDescribe("[sig-compute][virtctl] mass machine type transition", decorators.SigCompute, func() {
	var virtClient kubecli.KubevirtClient
	var err error

	createVM := func(virtClient kubecli.KubevirtClient, machineType, namespace string, hasLabel, running bool) *v1.VirtualMachine {
		template := tests.NewRandomVMI()
		template.ObjectMeta.Namespace = namespace
		template.Spec.Domain.Machine = &v1.Machine{Type: machineType}
		if hasLabel {
			template.ObjectMeta.Labels = map[string]string{"testing-label": "true"}
		}

		vm := tests.NewRandomVirtualMachine(template, running)
		vm, err = virtClient.VirtualMachine(util.NamespaceTestDefault).Create(context.Background(), vm)
		Expect(err).ToNot(HaveOccurred())

		if running {
			vm = tests.StartVMAndExpectRunning(virtClient, vm)
		}

		return vm
	}

	expectJobExists := func() *batchv1.Job {
		var job *batchv1.Job
		var ok bool

		Eventually(func() bool {
			jobs, err := virtClient.BatchV1().Jobs("kubevirt").List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			job, ok = hasJob(jobs)
			return ok
		}).Should(BeTrue())

		return job
	}

	deleteJob := func(job *batchv1.Job) {
		propagationPolicy := metav1.DeletePropagationBackground
		err = virtClient.BatchV1().Jobs("kubevirt").Delete(context.Background(), job.Name, metav1.DeleteOptions{
			PropagationPolicy: &propagationPolicy,
		})
		Expect(err).ToNot(HaveOccurred())
	}

	deleteVM := func(vm *v1.VirtualMachine) {
		err = virtClient.VirtualMachine(vm.Namespace).Delete(context.Background(), vm.Name, &metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		fmt.Printf("getting kv client")
		virtClient, err = kubecli.GetKubevirtClient()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("should update the machine types of outdated VMs", func() {
		It("no optional arguments are passed to virtctl command", Label("virtctl-update"), func() {
			vmNeedsUpdateStopped := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, false)
			vmNeedsUpdateRunning := createVM(virtClient, machineTypeNeedsUpdate, util.NamespaceTestDefault, false, true)
			vmNoUpdate := createVM(virtClient, machineTypeNoUpdate, util.NamespaceTestDefault, false, false)

			var vmList *v1.VirtualMachineList
			vmList, err = virtClient.VirtualMachine(metav1.NamespaceAll).List(context.Background(), &metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vmList.Items).To(HaveLen(3))

			err := clientcmd.NewRepeatableVirtctlCommand(update, machineTypes, setFlag(machineTypeFlag, machineTypeGlob))()
			Expect(err).ToNot(HaveOccurred())

			job := expectJobExists()

			Eventually(ThisVM(vmNeedsUpdateStopped), 60*time.Second, 1*time.Second).Should(beUpdated())
			Eventually(ThisVM(vmNeedsUpdateRunning), 60*time.Second, 1*time.Second).Should(beUpdated())
			Eventually(ThisVM(vmNoUpdate), 60*time.Second, 1*time.Second).Should(notBeUpdated(machineTypeNoUpdate))

			Eventually(ThisVM(vmNeedsUpdateRunning), 60*time.Second, 1*time.Second).Should(haveRestartLabel())

			Consistently(thisJob(virtClient, job), 300*time.Second, 1*time.Second).Should(haveEmptyCompletionTime())

			tests.StopVirtualMachine(vmNeedsUpdateRunning)
			deleteJob(job)
			deleteVM(vmNeedsUpdateStopped)
			deleteVM(vmNeedsUpdateRunning)
			deleteVM(vmNoUpdate)
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

			Eventually(ThisVM(vmNamespaceDefaultStopped), 60*time.Second, 1*time.Second).Should(beUpdated())
			Eventually(ThisVM(vmNamespaceDefaultRunning), 60*time.Second, 1*time.Second).Should(beUpdated())
			Eventually(ThisVM(vmNamespaceOtherStopped), 60*time.Second, 1*time.Second).Should(notBeUpdated(machineTypeNeedsUpdate))
			Eventually(ThisVM(vmNamespaceOtherRunning), 60*time.Second, 1*time.Second).Should(notBeUpdated(machineTypeNeedsUpdate))

			Eventually(ThisVM(vmNamespaceDefaultRunning), 60*time.Second, 1*time.Second).ShouldNot(haveRestartLabel())
			Eventually(ThisVM(vmNamespaceOtherRunning), 60*time.Second, 1*time.Second).Should(haveRestartLabel())

			tests.StopVirtualMachine(vmNamespaceDefaultRunning)
			tests.StopVirtualMachine(vmNamespaceOtherRunning)
			deleteJob(job)
			deleteVM(vmNamespaceDefaultStopped)
			deleteVM(vmNamespaceDefaultRunning)
			deleteVM(vmNamespaceOtherStopped)
			deleteVM(vmNamespaceOtherRunning)
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

			Expect(vmNoLabelStopped.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))
			Expect(vmWithLabelStopped.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmWithLabelRunning.Spec.Template.Spec.Domain.Machine).To(BeNil())
			Expect(vmNoLabelRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNeedsUpdate))

			Expect(vmWithLabelRunning.Labels).To(HaveKey(restartRequiredLabel))
			Expect(vmNoLabelRunning.Labels).ToNot(HaveKey(restartRequiredLabel))

			tests.StopVirtualMachine(vmNoLabelRunning)
			tests.StopVirtualMachine(vmWithLabelRunning)
			deleteJob(job)
			deleteVM(vmWithLabelStopped)
			deleteVM(vmWithLabelRunning)
			deleteVM(vmNoLabelStopped)
			deleteVM(vmNoLabelRunning)
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

			tests.StopVirtualMachine(vmNeedsUpdateRunning)
			deleteJob(job)
			deleteVM(vmNeedsUpdateStopped)
			deleteVM(vmNeedsUpdateRunning)
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
			Expect(vmNoUpdate.Spec.Template.Spec.Domain.Machine.Type).To(Equal(machineTypeNoUpdate))

			Eventually(func() bool {
				vm, err := virtClient.VirtualMachine(util.NamespaceTestDefault).Get(context.Background(), vmNamespaceDefaultWithLabelRunning.Name, &metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return *vm.Spec.Running
			}).Should(BeTrue())

			vmi, err := virtClient.VirtualMachineInstance(util.NamespaceTestDefault).Get(context.Background(), vmNamespaceDefaultWithLabelRunning.Name, &metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(vmNamespaceDefaultWithLabelRunning.Spec.Template.Spec.Domain.Machine.Type).To(Equal(virtconfig.DefaultAMD64MachineType))
			Expect(vmi.Spec.Domain.Machine.Type).To(Equal(virtconfig.DefaultAMD64MachineType))

			tests.StopVirtualMachine(vmNamespaceDefaultRunning)
			tests.StopVirtualMachine(vmNamespaceOtherRunning)
			tests.StopVirtualMachine(vmNamespaceDefaultWithLabelRunning)
			tests.StopVirtualMachine(vmNamespaceOtherWithLabelRunning)
			deleteJob(job)
			deleteVM(vmNamespaceDefaultStopped)
			deleteVM(vmNamespaceDefaultRunning)
			deleteVM(vmNamespaceOtherStopped)
			deleteVM(vmNamespaceOtherRunning)
			deleteVM(vmNamespaceDefaultWithLabelStopped)
			deleteVM(vmNamespaceDefaultWithLabelRunning)
			deleteVM(vmNamespaceOtherWithLabelStopped)
			deleteVM(vmNamespaceOtherWithLabelRunning)
			deleteVM(vmNoUpdate)
		})
	})
})

func hasJob(jobs *batchv1.JobList) (*batchv1.Job, bool) {
	for _, job := range jobs.Items {
		if strings.Contains(job.Name, "convert-machine-type") {
			return &job, true
		}
	}

	return nil, false
}

func notBeUpdated(machineType string) types.GomegaMatcher {
	return gcustom.MakeMatcher(func(actualVM *v1.VirtualMachine) (bool, error) {
		machine := actualVM.Spec.Template.Spec.Domain.Machine
		if machine != nil && machine.Type == machineType {
			return true, nil
		}
		return false, nil
	}).WithTemplate("Expected:\n{{.Actual}}\n{{.To}} to have machine type:\n{{.Data}}", machineType)
}

func beUpdated() types.GomegaMatcher {
	return gcustom.MakeMatcher(func(actualVM *v1.VirtualMachine) (bool, error) {
		machine := actualVM.Spec.Template.Spec.Domain.Machine
		if machine != nil && machine.Type == "q35" {
			return true, nil
		}
		return false, nil
	}).WithTemplate("Expected: machine type of \n{{.Actual}}\n{{.To}} to be nil")
}

func haveRestartLabel() types.GomegaMatcher {
	return gstruct.PointTo(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"ObjectMeta": gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"Labels": HaveKey(restartRequiredLabel),
		}),
	}))
}

func thisJob(virtClient kubecli.KubevirtClient, job *batchv1.Job) func() (*batchv1.Job, error) {
	return func() (j *batchv1.Job, err error) {
		return virtClient.BatchV1().Jobs(job.Namespace).Get(context.Background(), job.Name, metav1.GetOptions{})
	}
}

func haveEmptyCompletionTime() types.GomegaMatcher {
	return gstruct.PointTo(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
		"Status": gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"CompletionTime": BeNil(),
		}),
	}))
}
