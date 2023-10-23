package kor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func retreiveUsedPvcs(clientset kubernetes.Interface, namespace string) ([]string, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Failed to list Pods: %v\n", err)
		os.Exit(1)
	}
	var usedPvcs []string
	// Iterate through each Pod and check for PVC usage
	for _, pod := range pods.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim != nil {
				usedPvcs = append(usedPvcs, volume.PersistentVolumeClaim.ClaimName)
			}
		}
	}
	return usedPvcs, err
}

func processNamespacePvcs(clientset kubernetes.Interface, namespace string, opts *FilterOptions) ([]string, error) {
	pvcs, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pvcNames := make([]string, 0, len(pvcs.Items))
	for _, pvc := range pvcs.Items {
		if pvc.Labels["kor/used"] == "true" {
			continue
		}

		// checks if the resource has any labels that match the excluded selector specified in opts.ExcludeLabels.
		// If it does, the resource is skipped.
		if excluded, _ := HasExcludedLabel(pvc.Labels, opts.ExcludeLabels); excluded {
			continue
		}
		// checks if the resource’s age (measured from its creation time) falls within the range specified by opts.MinAge
		// and opts.MaxAge. If it doesn’t, the resource is skipped.
		if !HasIncludedAge(pvc.CreationTimestamp, opts) {
			continue
		}
		// checks if the resource’s size falls within the range specified by opts.MinSize and opts.MaxSize.
		// If it doesn’t, the resource is skipped.
		if included, _ := HasIncludedSize(pvc, opts); !included {
			continue
		}

		pvcNames = append(pvcNames, pvc.Name)
	}

	usedPvcs, err := retreiveUsedPvcs(clientset, namespace)
	if err != nil {
		return nil, err
	}

	diff := CalculateResourceDifference(usedPvcs, pvcNames)
	return diff, nil
}

func GetUnusedPvcs(includeExcludeLists IncludeExcludeLists, opts *FilterOptions, clientset kubernetes.Interface, outputFormat string, slackOpts SlackOpts) (string, error) {
	var outputBuffer bytes.Buffer
	namespaces := SetNamespaceList(includeExcludeLists, clientset)
	response := make(map[string]map[string][]string)

	for _, namespace := range namespaces {
		diff, err := processNamespacePvcs(clientset, namespace, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to process namespace %s: %v\n", namespace, err)
			continue
		}
		output := FormatOutput(namespace, diff, "Pvcs")

		outputBuffer.WriteString(output)
		outputBuffer.WriteString("\n")

		resourceMap := make(map[string][]string)
		resourceMap["Pvc"] = diff
		response[namespace] = resourceMap
	}

	jsonResponse, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "", err
	}

	unusedPvcs, err := unusedResourceFormatter(outputFormat, outputBuffer, slackOpts, jsonResponse)
	if err != nil {
		fmt.Printf("err: %v\n", err)
	}

	return unusedPvcs, nil
}
