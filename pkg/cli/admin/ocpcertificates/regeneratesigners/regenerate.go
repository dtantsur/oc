package regeneratesigners

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/oc/pkg/cli/admin/ocpcertificates/certgraphanalysis"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"k8s.io/apimachinery/pkg/runtime"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/cli-runtime/pkg/resource"
)

const (
	RegenerateSignersFieldManager = "regenerate-signers"
)

var (
	secretKind = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}
)

type RegenerateSignersRuntime struct {
	ResourceFinder genericclioptions.ResourceFinder
	KubeClient     kubernetes.Interface

	DryRun bool

	Printer printers.ResourcePrinter
	genericclioptions.IOStreams
}

func (o *RegenerateSignersRuntime) Run(ctx context.Context) error {
	visitor := o.ResourceFinder.Do()

	// TODO need to wire context through the visitorFns
	err := visitor.Visit(o.forceRegenerationOnResourceInfo)
	if err != nil {
		return err
	}
	return nil
}

// ought to find some way to test this.
func (o *RegenerateSignersRuntime) forceRegenerationOnResourceInfo(info *resource.Info, err error) error {
	if err != nil {
		return err
	}

	if secretKind != info.Object.GetObjectKind().GroupVersionKind() {
		return fmt.Errorf("command must only be pointed at secrets")
	}

	uncastObj, ok := info.Object.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("not unstructured: %w", err)
	}
	secret := &corev1.Secret{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(uncastObj.Object, secret); err != nil {
		return fmt.Errorf("not a secret: %w", err)
	}

	return o.forceRegenerationOnSecret(secret)
}

// split here for convenience of unit testing
func (o *RegenerateSignersRuntime) forceRegenerationOnSecret(secret *corev1.Secret) error {
	if len(secret.Annotations[certrotation.CertificateIssuer]) == 0 {
		// TODO this should return an error if the name was specified.
		// otherwise, not for this command.
		return nil
	}

	keyPairInfo, err := certgraphanalysis.InspectSecret(secret)
	if err != nil {
		return fmt.Errorf("error interpretting content: %w", err)
	}
	if keyPairInfo.Spec.Details.SignerDetails == nil {
		// not for this command.
		return nil
	}
	issuerInfo := keyPairInfo.Spec.CertMetadata.CertIdentifier.Issuer
	if issuerInfo == nil {
		// not for this command.
		return nil
	}

	if issuerInfo.CommonName != keyPairInfo.Spec.CertMetadata.CertIdentifier.CommonName {
		// not for this command, we only want self-signed signers.
		//fmt.Printf("#### SKIPPING ns/%v secret/%v issuer=%v\n", secret.Namespace, secret.Name, keyPairInfo.Spec.CertMetadata.CertIdentifier.Issuer)
		return nil
	}

	applyOptions := metav1.ApplyOptions{
		Force:        true,
		FieldManager: RegenerateSignersFieldManager,
	}
	if o.DryRun {
		applyOptions.DryRun = []string{metav1.DryRunAll}
	}

	secretToApply := applycorev1.Secret(secret.Name, secret.Namespace)
	secretToApply.WithAnnotations(map[string]string{
		certrotation.CertificateNotAfterAnnotation: "force-regeneration",
	})
	finalObject, err := o.KubeClient.CoreV1().Secrets(secret.Namespace).Apply(context.TODO(), secretToApply, applyOptions)

	// required for printing
	finalObject.GetObjectKind().SetGroupVersionKind(secretKind)
	if err := o.Printer.PrintObj(finalObject, o.Out); err != nil {
		return err
	}

	return err
}
