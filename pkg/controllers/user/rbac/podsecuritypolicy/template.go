package podsecuritypolicy

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"

	v3 "github.com/rancher/types/apis/management.cattle.io/v3"
	v1beta12 "github.com/rancher/types/apis/policy/v1beta1"
	v12 "github.com/rancher/types/apis/rbac.authorization.k8s.io/v1"
	"github.com/rancher/types/config"
	"github.com/sirupsen/logrus"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	rbac "k8s.io/api/rbac/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

const policyByPSPTParentAnnotationIndex = "podsecuritypolicy.rbac.user.cattle.io/parent-annotation"
const clusterRoleByPSPTNameIndex = "podsecuritypolicy.rbac.user.cattle.io/pspt-name"

// RegisterTemplate propagates updates to pod security policy templates to their associated pod security policies.
// Ignores pod security policy templates not assigned to a cluster or project.
func RegisterTemplate(ctx context.Context, context *config.UserContext) {
	logrus.Infof("registering podsecuritypolicy template handler for cluster %v", context.ClusterName)

	policyInformer := context.Policy.PodSecurityPolicies("").Controller().Informer()
	policyIndexers := map[string]cache.IndexFunc{
		policyByPSPTParentAnnotationIndex: policyByPSPTParentAnnotation,
	}
	policyInformer.AddIndexers(policyIndexers)

	clusterRoleInformer := context.RBAC.ClusterRoles("").Controller().Informer()
	clusterRoleIndexer := map[string]cache.IndexFunc{
		clusterRoleByPSPTNameIndex: clusterRoleByPSPTName,
	}
	clusterRoleInformer.AddIndexers(clusterRoleIndexer)

	lfc := &Lifecycle{
		policies:          context.Policy.PodSecurityPolicies(""),
		policyLister:      context.Policy.PodSecurityPolicies("").Controller().Lister(),
		clusterRoles:      context.RBAC.ClusterRoles(""),
		clusterRoleLister: context.RBAC.ClusterRoles("").Controller().Lister(),

		policyIndexer:      policyInformer.GetIndexer(),
		clusterRoleIndexer: clusterRoleInformer.GetIndexer(),
	}

	pspti := context.Management.Management.PodSecurityPolicyTemplates("")
	psptSync := v3.NewPodSecurityPolicyTemplateLifecycleAdapter("cluster-pspt-sync_"+context.ClusterName, true, pspti, lfc)
	context.Management.Management.PodSecurityPolicyTemplates("").AddHandler(ctx, "pspt-sync", psptSync)
}

type Lifecycle struct {
	policies          v1beta12.PodSecurityPolicyInterface
	policyLister      v1beta12.PodSecurityPolicyLister
	clusterRoles      v12.ClusterRoleInterface
	clusterRoleLister v12.ClusterRoleLister

	policyIndexer      cache.Indexer
	clusterRoleIndexer cache.Indexer
}

func (l *Lifecycle) Create(obj *v3.PodSecurityPolicyTemplate) (runtime.Object, error) {
	return nil, nil
}

func (l *Lifecycle) Updated(obj *v3.PodSecurityPolicyTemplate) (runtime.Object, error) {
	policies, err := l.policyIndexer.ByIndex(policyByPSPTParentAnnotationIndex, obj.Name)
	if err != nil {
		return nil, fmt.Errorf("error getting policies: %v", err)
	}

	for _, rawPolicy := range policies {
		policy := rawPolicy.(*policyv1beta1.PodSecurityPolicy)

		if policy.Annotations[podSecurityPolicyTemplateVersionAnnotation] != obj.ResourceVersion {
			newPolicy := policy.DeepCopy()
			newPolicy.Spec = obj.Spec
			newPolicy.Annotations[podSecurityPolicyTemplateVersionAnnotation] = obj.ResourceVersion

			_, err = l.policies.Update(newPolicy)
			if err != nil {
				return nil, fmt.Errorf("error updating psp: %v", err)
			}
		}
	}

	return obj, nil
}

func (l *Lifecycle) Remove(obj *v3.PodSecurityPolicyTemplate) (runtime.Object, error) {
	policies, err := l.policyIndexer.ByIndex(policyByPSPTParentAnnotationIndex, obj.Name)
	if err != nil {
		return nil, fmt.Errorf("error getting policies: %v", err)
	}

	for _, rawPolicy := range policies {
		policy := rawPolicy.(*policyv1beta1.PodSecurityPolicy)
		err = l.policies.Delete(policy.Name, &v1.DeleteOptions{})
		if err != nil {
			return nil, fmt.Errorf("error deleting policy: %v", err)
		}
	}

	clusterRoles, err := l.clusterRoleIndexer.ByIndex(clusterRoleByPSPTNameIndex, obj.Name)
	if err != nil {
		return nil, fmt.Errorf("error getting cluster roles: %v", err)
	}

	for _, rawClusterRole := range clusterRoles {
		clusterRole := rawClusterRole.(*rbac.ClusterRole)
		err = l.clusterRoles.DeleteNamespaced(clusterRole.Namespace, clusterRole.Name, &v1.DeleteOptions{})
		if err != nil {
			return nil, fmt.Errorf("error deleting cluster role: %v", err)
		}
	}

	return obj, nil
}

func policyByPSPTParentAnnotation(obj interface{}) ([]string, error) {
	policy, ok := obj.(*policyv1beta1.PodSecurityPolicy)
	if !ok || policy.Annotations[podSecurityPolicyTemplateParentAnnotation] == "" {
		return []string{}, nil
	}

	return []string{policy.Annotations[podSecurityPolicyTemplateParentAnnotation]}, nil
}

func clusterRoleByPSPTName(obj interface{}) ([]string, error) {
	clusterRole, ok := obj.(*rbac.ClusterRole)
	if !ok || clusterRole.Annotations[podSecurityPolicyTemplateParentAnnotation] == "" {
		return []string{}, nil
	}

	return []string{clusterRole.Annotations[podSecurityPolicyTemplateParentAnnotation]}, nil
}
