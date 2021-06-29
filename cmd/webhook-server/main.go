/*
Copyright (c) 2019 StackRox Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	tlsDir      = `/Users/shenkonghui/src/pf/admission-controller-webhook/deployment/`
	tlsCertFile = `server.crt`
	tlsKeyFile  = `server.key`
)

var (
	podResource = metav1.GroupVersionResource{Version: "v1", Resource: "pods"}
)

// applySecurityDefaults implements the logic of our example admission controller webhook. For every pod that is created
// (outside of Kubernetes namespaces), it first checks if `runAsNonRoot` is set. If it is not, it is set to a default
// value of `false`. Furthermore, if `runAsUser` is not set (and `runAsNonRoot` was not initially set), it defaults
// `runAsUser` to a value of 1234.
//
// To demonstrate how requests can be rejected, this webhook further validates that the `runAsNonRoot` setting does
// not conflict with the `runAsUser` setting - i.e., if the former is set to `true`, the latter must not be `0`.
// Note that we combine both the setting of defaults and the check for potential conflicts in one webhook; ideally,
// the latter would be performed in a validating webhook admission controller.
func applySecurityDefaults(req *v1beta1.AdmissionRequest) ([]patchOperation, error) {
	// This handler should only get called on Pod objects as per the MutatingWebhookConfiguration in the YAML file.
	// However, if (for whatever reason) this gets invoked on an object of a different kind, issue a log message but
	// let the object request pass through otherwise.
	if req.Resource != podResource {
		log.Printf("expect resource to be %s", podResource)
		return nil, nil
	}

	// Parse the Pod object.
	raw := req.Object.Raw
	pod := corev1.Pod{}
	if _, _, err := universalDeserializer.Decode(raw, nil, &pod); err != nil {
		return nil, fmt.Errorf("could not deserialize pod object: %v", err)
	}

	var patches []patchOperation
	if pod.Labels["middleware"] != "redis"{
		return patches, nil
	}

	volumes := pod.Spec.Volumes

	for i:=0;i< len(volumes);i++{
		if volumes[i].PersistentVolumeClaim != nil{
			klog.Infof("change pod %s claimname %s -> %s",pod.Name,volumes[i].PersistentVolumeClaim.ClaimName,fmt.Sprintf("%s-replica",volumes[i].PersistentVolumeClaim.ClaimName))

			volumes[i].PersistentVolumeClaim.ClaimName = fmt.Sprintf("%s-replica",volumes[i].PersistentVolumeClaim.ClaimName)

			config, _ := clientcmd.BuildConfigFromFlags("", filepath.Join(os.Getenv("HOME"), ".kube", "config"))
			clientset, _ := kubernetes.NewForConfig(config)
			_,err := clientset.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(context.TODO(),volumes[i].PersistentVolumeClaim.ClaimName,metav1.GetOptions{} )

			if err!=nil{
				if errors.IsNotFound(err){
					sts,_ := clientset.AppsV1().StatefulSets(pod.Namespace).Get(context.TODO(),pod.OwnerReferences[0].Name,metav1.GetOptions{})

					pvc := corev1.PersistentVolumeClaim{}
					pvc.Namespace = pod.Namespace
					pvc.Name = volumes[i].PersistentVolumeClaim.ClaimName
					pvc.Spec = sts.Spec.VolumeClaimTemplates[0].Spec
					klog.Infof("create pvc %s",pvc.Name)
					clientset.CoreV1().PersistentVolumeClaims(pod.Namespace).Create(context.TODO(),&pvc,metav1.CreateOptions{})
				}
			}
		}
	}

	var nodeName = "slave-213"
	klog.Infof("asssign pod %s to node %s",pod.Name,nodeName)
	nodeSelector := pod.Spec.NodeSelector
	if nodeSelector == nil{
		nodeSelector = make(map[string]string)
	}
	nodeSelector["kubernetes.io/hostname"] = nodeName

	patches = append(patches, patchOperation{
		Op:   "replace",
		Path: "/spec/volumes",
		Value: volumes,
	})
	patches = append(patches, patchOperation{
		Op:   "replace",
		Path: "/spec/nodeSelector",
		Value: nodeSelector,
	})
	return patches, nil
}

func main() {
	certPath := filepath.Join(tlsDir, tlsCertFile)
	keyPath := filepath.Join(tlsDir, tlsKeyFile)

	mux := http.NewServeMux()
	mux.Handle("/mutate", admitFuncHandler(applySecurityDefaults))
	server := &http.Server{
		Addr:    ":8443",
		Handler: mux,
	}
	log.Fatal(server.ListenAndServeTLS(certPath, keyPath))
}
