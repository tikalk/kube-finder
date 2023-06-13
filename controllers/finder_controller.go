/*
Copyright 2023.

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
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/slack-go/slack"
	kubefinderv1alpha1 "github.com/tikalk/kube-finder/api/v1alpha1"
	"github.com/tmc/langchaingo/llms/openai"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// FinderReconciler reconciles a Finder object
type FinderReconciler struct {
	client.Client
	Scheme                   *runtime.Scheme
	logger                   logr.Logger
	ActiveKubeFinderHandlers map[string]*KubeFinderHandler
}

//+kubebuilder:rbac:groups=kubefinder.tikalk.com,resources=finders,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubefinder.tikalk.com,resources=finders/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubefinder.tikalk.com,resources=finders/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Finder object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *FinderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.logger = log.FromContext(ctx)
	r.logger.Info(fmt.Sprintf("Reconciling Finder %s/%s", req.Namespace, req.Name))

	// get the object from the api server
	kubeFinder := &kubefinderv1alpha1.Finder{}
	namespaceName := fmt.Sprintf("%s/%s", req.Namespace, req.Name)

	if err := r.Get(ctx, req.NamespacedName, kubeFinder); err != nil {
		if errors.IsNotFound(err) {
			r.logger.Info(fmt.Sprintf("kubeFinder object %s has been deleted. Removing handler...", namespaceName))
			r.removeKubeFinderHandler(namespaceName)
			return ctrl.Result{}, nil
		}
		r.logger.Error(err, "unable to fetch Finder")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// if kubeFinder is being updated, remove it from the active kubeFinderHandlers in order to create a new handler.
	if KubeFinderHandler := r.findKubeFinderHandler(namespaceName); KubeFinderHandler != nil {
		if reflect.DeepEqual(kubeFinder.Spec, KubeFinderHandler.kubeFinder.Spec) {
			// if spec has not changed, ignore the update.
			r.logger.Info(fmt.Sprintf("kubeFinder spec has not changed <%s>. Ignoring...", req.NamespacedName))
			return ctrl.Result{}, nil
		}
		// if spec has changed, remove the handler.
		r.logger.Info(fmt.Sprintf("KubeFinder object updated <%s>. Removing handler...", namespaceName))
		r.removeKubeFinderHandler(namespaceName)
	}

	// create a new KubeFinderHandler.
	KubeFinderHandler, err := newKubeFinderHandler(*kubeFinder, *r)
	if err != nil {
		r.logger.Error(err, "unable to create KubeFinderHandler")
		return ctrl.Result{}, err
	}

	// add KubeFinderHandler to the active kubeFinderHandlers.
	r.registerAndRunKubeFinderHandler(namespaceName, KubeFinderHandler)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FinderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubefinderv1alpha1.Finder{}).
		Complete(r)
}

func filterTimeByStartTime(seconds int, pod corev1.Pod) bool {
	now := time.Now()
	threshold := now.Add(-time.Duration(seconds) * time.Second)
	return pod.CreationTimestamp.Unix() > threshold.Unix()
}

func (r *FinderReconciler) handlePods(ctx context.Context, kubeFinder *kubefinderv1alpha1.Finder) (err error) {
	// get all pods in all namespaces.
	podList, err := r.ListPods(ctx)
	if err != nil {
		return err
	}

	for _, pod := range podList.Items {
		podNamespacedName := getNamespacedName(pod)

		switch pod.Status.Phase {
		case corev1.PodPending: // TODO: check if the pod is already alive for more than 5 min in 'Pending' state for longer than 5 minutes.
			if r.isPodFound(pod, kubeFinder.Status.FoundPods) {
				r.logger.Info(fmt.Sprintf("Pod Name: %s already found", podNamespacedName))
				continue
			}

			r.logger.Info(fmt.Sprintf("Found new pod,  name: %s, Pod status: %s", podNamespacedName, pod.Status.Phase))

			// check if the pod is stuck in 'Pending' state for longer than 5 minutes.
			if filterTimeByStartTime(10, pod) {
				r.logger.Info(fmt.Sprintf("ignoring new pod '%s'.", podNamespacedName))
				continue
			}

			// get pod events.
			events, err := r.GetPodEvents(pod)
			if err != nil {
				return err
			}

			// get all events messages.
			var totalEvents []string
			for _, event := range events.Items {
				totalEvents = append(totalEvents, event.Message)
			}

			// get answer from GPT-3.
			question := fmt.Sprintf("hey, i'm using kubernetes and got this errors: %s \n, please provide short explaination about the issue, together with steps for a solution. please provide any kubernetes commands that can help. the output should be readable and not longer than 100 letters. also, please add new line between the steps, if there are any commands, wrap commands in code block", totalEvents)
			answer, err := r.AskGPT(question)
			if err != nil {
				r.logger.Error(err, "Got error while looking for answer")
				return err
			}
			//r.logger.Info(fmt.Sprintf("\n\n\nGot Answer: %s\n\n\n", answer))

			if strings.Contains(answer, "Sorry") {
				// the answer includes "Sorry"
				r.logger.Info(fmt.Sprintf("\n\n\nAnswer not completed: %s\n\n\n", answer))
				continue
			}

			// send Slack notification.
			err = r.SendSlackNotification(podNamespacedName, pod.Kind, answer, kubeFinder.Spec.Notify.Slack.ChannelID)
			if err != nil {
				r.logger.Error(err, "Got error while sending slack notification")
				return err
			}

			// update kubeFinder status with the new pod found.
			err = r.updateNewFound(ctx, kubeFinder, pod, totalEvents)
			if err != nil {
				r.logger.Error(err, "Got error while update kubeFinder status")
				return err
			}
		case corev1.PodRunning:
			// check if in finder event
			if r.isPodFound(pod, kubeFinder.Status.FoundPods) {
				r.logger.Info(fmt.Sprintf("Found pod %s in KubeFinder %s\n", podNamespacedName, kubeFinder.Name))
				// if in finder remove
				r.removeFound(ctx, kubeFinder, pod)
				r.logger.Info(fmt.Sprintf("Removed pod %s from KubeFinder %s events\n", podNamespacedName, kubeFinder.Name))
			}
		case corev1.PodFailed:
			r.logger.Info(fmt.Sprintf("Found Pod Name: %s, Pod Status: %s", pod.Name, pod.Status.Phase))
		}
	}

	return nil
}
func (r *FinderReconciler) ListPods(ctx context.Context) (pods corev1.PodList, err error) {
	// get all pods in all namespaces.
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList); err != nil {
		r.logger.Error(err, "unable to list pods")
		return *podList, err
	}
	// loop over each pod status field and print pod statu

	return *podList, nil
}

func (r *FinderReconciler) GetPodEvents(pod corev1.Pod) (list corev1.EventList, err error) {
	// get events from pod.
	events := &corev1.EventList{}
	if err := r.List(context.Background(), events, client.InNamespace(pod.Namespace), client.MatchingFields{"involvedObject.name": pod.GetName()}); err != nil {
		r.logger.Error(err, "unable to list events")
		return *events, err
	}
	return *events, nil
}

func (r *FinderReconciler) AskGPT(question string) (answer string, err error) {
	apiKey := r.getSecretData("kube-finder-secret", "kube-finder", "openai-api-key")
	llm, err := openai.New(openai.WithToken(apiKey))
	if err != nil {
		r.logger.Error(err, "unable to create openai client")
		return "", err
	}
	ctx := context.Background()
	completion, err := llm.Call(ctx, question) // the model to use for the completion
	if err != nil {
		r.logger.Error(err, "unable to call openai")
	}
	return completion, nil
}

func (r *FinderReconciler) SendSlackNotification(resourceName string, resourceKind string, message string, channelID string) (err error) {
	messages := map[string]string{
		"gptAnswer": fmt.Sprintf("I found some issue with '%s' %s: \n %s", resourceName, resourceKind, message),
	}

	colors := map[string]string{
		"gptAnswer": "#36a64f",
		"error":     "#ff0000",
	}
	token := r.getSecretData("kube-finder-secret", "kube-finder", "slack-token")
	slackClient := slack.New(token)
	// Create the Slack attachment that we will send to the channel
	attachment := slack.Attachment{
		AuthorName: "kube-finder:",
		Text:       messages["gptAnswer"],
		Color:      colors["gptAnswer"],
		MarkdownIn: []string{resourceName},
		Fields: []slack.AttachmentField{
			{Title: "Involve Resource:"},
			{Value: fmt.Sprintf("%s - %s", resourceKind, resourceName), Short: true},
		}}

	channelID, timestamp, err := slackClient.PostMessage(
		channelID,
		slack.MsgOptionAttachments(attachment),
	)

	if err != nil {
		r.logger.Error(err, "got error while sending slack message")
		return
	}

	r.logger.Info(fmt.Sprintf("message successfully sent to channel %s at %s", channelID, timestamp))
	return nil

}

func (r *FinderReconciler) removeFound(ctx context.Context, kubeFinder *kubefinderv1alpha1.Finder, obj interface{}) (err error) {

	// fetch the latest Finder state from the server to avoid update conflicts
	upToDateKubeFinder := &kubefinderv1alpha1.Finder{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      kubeFinder.Name,
		Namespace: kubeFinder.Namespace,
	}, upToDateKubeFinder); err != nil {
		// object no longer active, probably deleted, don't try to update, and return nil
		if errors.IsNotFound(err) {
			return nil
		}
		r.logger.Error(err, "unable to fetch Finder")
		return err
	}

	switch obj := obj.(type) {
	case corev1.Pod:
		// remove pod from FoundPods map.
		delete(upToDateKubeFinder.Status.FoundPods, getNamespacedName(obj))
	case corev1.Service:
		// init FoundServices map if not exist.

	}

	err = r.Client.Status().Update(ctx, upToDateKubeFinder)
	if err != nil {
		return err
	}
	return nil
}

func (r *FinderReconciler) updateNewFound(ctx context.Context, kubeFinder *kubefinderv1alpha1.Finder, obj interface{}, events []string) (err error) {

	// init new FoundSpec.
	foundSpec := newFoundSpec(obj, events)

	switch obj := obj.(type) {
	case corev1.Pod:
		// init FoundPods map if not exist.
		if kubeFinder.Status.FoundPods == nil {
			kubeFinder.Status.FoundPods = make(map[string]kubefinderv1alpha1.FoundSpec)
		}
		// add new pod to FoundPods map.
		kubeFinder.Status.FoundPods[getNamespacedName(obj)] = *foundSpec
	case corev1.Service:
		// init FoundServices map if not exist.

	}

	// fetch the latest Finder state from the server to avoid update conflicts
	upToDateKubeFinder := &kubefinderv1alpha1.Finder{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      kubeFinder.Name,
		Namespace: kubeFinder.Namespace,
	}, upToDateKubeFinder); err != nil {
		// object no longer active, probably deleted, don't try to update, and return nil
		if errors.IsNotFound(err) {
			return nil
		}
		r.logger.Error(err, "unable to fetch Finder")
		return err
	}

	err = r.Client.Status().Update(ctx, kubeFinder)
	if err != nil {
		return err
	}
	return nil
}

// registerAndRunResourceManagerHandler add the handler to the collection and then run it
func (r *FinderReconciler) registerAndRunKubeFinderHandler(kubeFinderName string, kubeFinderHandler *KubeFinderHandler) {
	r.ActiveKubeFinderHandlers[kubeFinderName] = kubeFinderHandler
	go func() {
		err := r.ActiveKubeFinderHandlers[kubeFinderName].Run()
		if err != nil {
			r.logger.Error(err, "error while running kubeFinderHandler %s", kubeFinderName)
		}
	}()
}
func (r *FinderReconciler) findKubeFinderHandler(kubeFinderName string) *KubeFinderHandler {
	return r.ActiveKubeFinderHandlers[kubeFinderName]
}

func (r *FinderReconciler) removeKubeFinderHandler(kubeFinderName string) {
	if _, ok := r.ActiveKubeFinderHandlers[kubeFinderName]; ok {
		r.ActiveKubeFinderHandlers[kubeFinderName].Stop()
		delete(r.ActiveKubeFinderHandlers, kubeFinderName)
	}
}

func (r *FinderReconciler) isPodFound(pod corev1.Pod, FoundPods map[string]kubefinderv1alpha1.FoundSpec) bool {
	if _, ok := FoundPods[getNamespacedName(pod)]; ok {
		return true
	}
	return false
}

func getNamespacedName(obj interface{}) string {
	switch obj := obj.(type) {
	case corev1.Pod:
		return fmt.Sprintf("%s/%s", obj.Namespace, obj.Name)
	case corev1.Service:
		return fmt.Sprintf("%s/%s", obj.Namespace, obj.Name)
	}
	panic(fmt.Sprintf("unknown object type: %T", obj))

}

func newFoundSpec(obj interface{}, events []string) *kubefinderv1alpha1.FoundSpec {
	switch obj := obj.(type) {
	case corev1.Pod:
		return &kubefinderv1alpha1.FoundSpec{
			Name:       obj.Name,
			Namespace:  obj.Namespace,
			ObjectType: obj.Kind,
			Message:    obj.Status.Message,
			Events:     events,
		}

	case corev1.Service:
		return &kubefinderv1alpha1.FoundSpec{
			Name:       obj.Name,
			Namespace:  obj.Namespace,
			ObjectType: obj.Kind,
			Message:    obj.Spec.ClusterIP,
			Events:     events,
		}
	}
	panic(fmt.Sprintf("unknown object type: %T", obj))
}

func (r *FinderReconciler) getSecretData(name string, namespace string, key string) (data string) {
	// get secret
	secretName := types.NamespacedName{Namespace: namespace, Name: name}
	secret := &corev1.Secret{}
	err := r.Client.Get(context.Background(), secretName, secret)
	if err != nil {
		r.logger.Error(err, "unable to get secret")
		return
	}

	r.logger.Info(fmt.Sprintf("secret: '%s' retrieved", secret.Name))

	// strip the secret data
	return strings.Trim(string(secret.Data[key]), "\n")

}
