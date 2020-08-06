package k8s

import (
	"path"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/pkg/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func getConfig() (*rest.Config, error) {
	var cfg *rest.Config
	// creates the in-cluster config
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// creates the out-of-cluster config
		masterURL := ""
		kubeconfigPath := path.Join(homedir.HomeDir(), ".kube/config")
		cfg, err = clientcmd.BuildConfigFromFlags(masterURL, kubeconfigPath)
		if err != nil {
			return nil, errors.New("Failed to get k8s config")
		}
	}

	return cfg, nil
}

func GetClientSet() (*kubernetes.Clientset, error) {
	cfg, err := getConfig()
	if err != nil {
		return nil, errors.Wrap(err, "GetK8sConfig")
	}

	clientSet, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "kubernetes.NewForConfig")
	}

	return clientSet, nil
}

func GetDynamicClient() (dynamic.Interface, error) {
	cfg, err := getConfig()
	if err != nil {
		return nil, errors.Wrap(err, "GetK8sConfig")
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "dynamic.NewForConfig")
	}

	return dynamicClient, nil
}

func GetCRList(dynamicClient *dynamic.Interface, GVR *schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	CRs, err := (*dynamicClient).Resource(*GVR).Namespace(namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "dynamicClient.Resource.Namespace.List")
	}

	return CRs, nil
}

func GetCRListWithLabels(dynamicClient *dynamic.Interface, GVR *schema.GroupVersionResource, namespace string, listOptions metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	CRs, err := (*dynamicClient).Resource(*GVR).Namespace(namespace).List(listOptions)
	if err != nil {
		return nil, errors.Wrap(err, "dynamicClient.Resource.Namespace.List")
	}

	return CRs, nil
}

func GetCR(dynamicClient *dynamic.Interface, GVR *schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	CR, err := (*dynamicClient).Resource(*GVR).Namespace(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "dynamicClient.Resource.Namespace.Get")
	}

	return CR, nil
}

func CreateCR(dynamicClient *dynamic.Interface, GVR *schema.GroupVersionResource, namespace string, obj *unstructured.Unstructured) error {
	_, err := (*dynamicClient).Resource(*GVR).Namespace(namespace).Create(obj, metav1.CreateOptions{})
	if err != nil {
		return errors.Wrap(err, "dynamicClient.Resource.Namespace.Create")
	}

	return nil
}

func UpdateCR(dynamicClient *dynamic.Interface, GVR *schema.GroupVersionResource, namespace string, obj *unstructured.Unstructured) error {
	_, err := (*dynamicClient).Resource(*GVR).Namespace(namespace).Update(obj, metav1.UpdateOptions{})
	if err != nil {
		return errors.Wrap(err, "dynamicClient.Resource.Namespace.Update")
	}

	return nil
}

func DeleteCR(dynamicClient *dynamic.Interface, GVR *schema.GroupVersionResource, namespace, objName string) error {
	err := (*dynamicClient).Resource(*GVR).Namespace(namespace).Delete(objName, &metav1.DeleteOptions{})
	if err != nil {
		return errors.Wrap(err, "dynamicClient.Resource.Namespace.Delete")
	}

	return nil
}
