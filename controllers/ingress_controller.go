/*


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

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	networking "istio.io/api/networking/v1alpha3"
	networkingclient "istio.io/client-go/pkg/apis/networking/v1alpha3"
	versionedclient "istio.io/client-go/pkg/clientset/versioned"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	name      = "challenge"
	namespace = "kubeflow"
)

// IngressReconciler reconciles a Ingress object
type IngressReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	IstioClient *versionedclient.Clientset
}

func (r *IngressReconciler) ReconcileGateway(ctx context.Context) error {
	create := false
	gw, err := r.IstioClient.NetworkingV1alpha3().Gateways(namespace).Get(ctx, name, v1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		gw = &networkingclient.Gateway{}
		create = true

	}
	gw.SetNamespace(namespace)
	gw.SetName(name)
	gw.Spec = networking.Gateway{
		Selector: map[string]string{"istio": "ingressgateway"},
		Servers: []*networking.Server{
			&networking.Server{
				Hosts: []string{"*"},
				Port: &networking.Port{
					Number:   80,
					Protocol: "HTTP",
					Name:     "http",
				},
			},
		},
	}

	if create {
		_, err = r.IstioClient.NetworkingV1alpha3().Gateways(namespace).Create(ctx, gw, v1.CreateOptions{})
	} else {
		_, err = r.IstioClient.NetworkingV1alpha3().Gateways(namespace).Update(ctx, gw, v1.UpdateOptions{})
	}
	return err
}

func (r *IngressReconciler) ReconcileVirtualService(ctx context.Context, ingress *networkingv1beta1.Ingress) error {
	create := false

	vs, err := r.IstioClient.NetworkingV1alpha3().VirtualServices(namespace).Get(ctx, name, v1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		vs = &networkingclient.VirtualService{}
		create = true
	}
	vs.SetName(name)
	vs.SetNamespace(namespace)
	rule := ingress.Spec.Rules[0]
	path := rule.HTTP.Paths[0]
	vs.Spec = networking.VirtualService{
		Hosts:    []string{rule.Host},
		Gateways: []string{name},
		Http: []*networking.HTTPRoute{
			&networking.HTTPRoute{
				Match: []*networking.HTTPMatchRequest{
					&networking.HTTPMatchRequest{
						Method: &networking.StringMatch{
							MatchType: &networking.StringMatch_Exact{
								Exact: "GET",
							},
						},
						Uri: &networking.StringMatch{
							MatchType: &networking.StringMatch_Prefix{
								Prefix: path.Path,
							},
						},
					},
				},
				Route: []*networking.HTTPRouteDestination{
					&networking.HTTPRouteDestination{
						Destination: &networking.Destination{
							Host: fmt.Sprintf("%s.%s.svc.cluster.local", path.Backend.ServiceName, ingress.Namespace),
							Port: &networking.PortSelector{
								Number: uint32(path.Backend.ServicePort.IntVal),
							},
						},
					},
				},
			},
		},
	}
	if create {
		_, err = r.IstioClient.NetworkingV1alpha3().VirtualServices(namespace).Create(ctx, vs, v1.CreateOptions{})
	} else {
		_, err = r.IstioClient.NetworkingV1alpha3().VirtualServices(namespace).Update(ctx, vs, v1.UpdateOptions{})
	}
	return err
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;create;delete;update
// +kubebuilder:rbac:groups=networking.istio.io,resources=gateways,verbs=get;list;create;delete;update
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get

func (r *IngressReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("ingress", req.NamespacedName)
	log.Info("reconcile", "request", req)

	ingress := &networkingv1beta1.Ingress{}
	if err := r.Get(ctx, req.NamespacedName, ingress); err != nil {
		if !errors.IsNotFound(err) {
			return reconcile.Result{}, err
		}
		log.Info("clean up", "request", req)
		return reconcile.Result{}, r.cleanup(ctx, req)
	}
	if !ingress.GetDeletionTimestamp().IsZero() {
		log.Info("clean up", "request", req)
		return reconcile.Result{}, r.cleanup(ctx, req)
	}
	log.Info("reconcile gateway", "request", req)
	if err := r.ReconcileGateway(ctx); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconcile virtual service", "request", req)
	if err := r.ReconcileVirtualService(ctx, ingress); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *IngressReconciler) cleanup(ctx context.Context, req ctrl.Request) error {
	if _, err := r.IstioClient.NetworkingV1alpha3().VirtualServices(namespace).Get(ctx, name, v1.GetOptions{}); err == nil {
		err := r.IstioClient.NetworkingV1alpha3().VirtualServices(namespace).Delete(ctx, name, v1.DeleteOptions{})
		if err != nil {
			return err
		}
	} else if !errors.IsNotFound(err) {
		return err
	}

	if _, err := r.IstioClient.NetworkingV1alpha3().Gateways(namespace).Get(ctx, name, v1.GetOptions{}); err == nil {
		err := r.IstioClient.NetworkingV1alpha3().Gateways(namespace).Delete(ctx, name, v1.DeleteOptions{})
		if err != nil {
			return err
		}
	} else if !errors.IsNotFound(err) {
		return err
	}

	return nil
}

func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1beta1.Ingress{}).
		Complete(r)
}
