package convertmachinetype

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	"kubevirt.io/client-go/kubecli"

	"kubevirt.io/kubevirt/pkg/virtctl/templates"
)

const (
	COMMAND_CONVERT_MACHINE_TYPE = "convert-machine-type"
)

type Command struct {
	clientConfig clientcmd.ClientConfig
	command      string
}

// holding flag information
var (
	namespaceFlag     string
	forceRestartFlag  bool
	labelSelectorFlag string
)

// NewMMTTCommand generates a new "convert-machine-types" command
func NewMMTTCommand(clientConfig clientcmd.ClientConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "convert-machine-type",
		Short: "Perform a mass machine type transition on any VMs that have an outdated machine type.",
		Long: `Create a Job that iterates through VMs, updating the machine type of any VMs that have an outdated machine type. If a VM is running, it will also label the VM with 'restart-vm-required=true', indicating the user will need to perform manually by default. If --force-restart is set to true, the VM will be automatically restarted and the label will be removed. The Job will terminate once all VMs have their machine types updated, and all 'restart-vm-required' labels have been cleared.
If no namespace is specified via --namespace, the mass machine type transition will be applied across all namespaces.
Note that should the Job fail, it will be restarted. Additonally, once the Job is terminated, it will not be automatically deleted. The Job can be monitored and then deleted manually after it has been terminated.`,
		Example: usage(),
		Args:    templates.ExactArgs("convert-machine-type", 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := Command{command: COMMAND_CONVERT_MACHINE_TYPE, clientConfig: clientConfig}
			return c.RunE(args)
		},
	}

	// flags for the "expose" command
	cmd.Flags().StringVar(&namespaceFlag, "namespace", "", "Namespace in which the mass machine type transition will be applied. Leave empty to apply to all namespaces.")
	cmd.Flags().BoolVar(&forceRestartFlag, "force-restart", false, "When true, restarts all VMs that have their machine types updated. Otherwise, updated VMs must be restarted manually for the machine type change to take effect.")
	cmd.Flags().StringVar(&labelSelectorFlag, "label-selector", "", "Selector (label query) on which to filter VMs to be updated.")
	cmd.SetUsageTemplate(templates.UsageTemplate())

	return cmd
}

func usage() string {
	usage := `  # Update the machine types of all VMs with an outdated machine type across all namespaces without automatically restarting running VMs:
  {{ProgramName}} convert-machine-type

  # Update the machine types of all VMs with an outdated machine type in the namespace 'default':
  {{ProgramName}} convert-machine-type --namespace=default

  # Update the machine types of all VMs with an outdated machine type and automatically restart them if they are running:
  {{ProgramName}} convert-machine-type --force-restart=true
  
  # Update the machine types of all VMs with the label 'kubevirt.io/memory=large':
  {{ProgramName}} convert-machine-type --label-selector=kubevirt.io/memory=large`
	return usage
}

// executing the "expose" command
func (o *Command) RunE(args []string) error {
	// get the namespace
	configNamespace, _, err := o.clientConfig.Namespace()
	if err != nil {
		return err
	}

	// get the client
	virtClient, err := kubecli.GetKubevirtClientFromClientConfig(o.clientConfig)
	if err != nil {
		return fmt.Errorf("cannot obtain KubeVirt client: %v", err)
	}

	job := generateMassMachineTypeTransitionJob()
	batch := virtClient.BatchV1()
	_, err = batch.Jobs(configNamespace).Create(context.Background(), job, metav1.CreateOptions{})
	return err
}

func generateMassMachineTypeTransitionJob() *batchv1.Job {
	job := &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "Job",
		},

		ObjectMeta: metav1.ObjectMeta{
			Name:      "convert-machine-type",
			Namespace: metav1.NamespaceDefault,
		},

		Spec: batchv1.JobSpec{
			Template: v1.PodTemplateSpec{
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "convert-machine-type-image",
							Image: "mmtt-image",
							Env: []v1.EnvVar{
								{
									Name:  "NAMESPACE",
									Value: namespaceFlag,
								},
								{
									Name:  "FORCE_RESTART",
									Value: strconv.FormatBool(forceRestartFlag),
								},
								{
									Name:  "LABEL_SELECTOR",
									Value: labelSelectorFlag,
								},
							},
						},
					},
					RestartPolicy: v1.RestartPolicyOnFailure,
				},
			},
		},
	}
	return job
}
