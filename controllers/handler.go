package controllers

import (
	"context"
	"fmt"
	kubefinderv1alpha1 "github.com/tikalk/kube-finder/api/v1alpha1"
	"time"
)

type KubeFinderHandler struct {
	kubeFinder       *kubefinderv1alpha1.Finder
	FinderReconciler *FinderReconciler
	namespaceName    string
	stopper          chan struct{}
}

func newKubeFinderHandler(kubeFinder kubefinderv1alpha1.Finder, FinderReconciler FinderReconciler) (*KubeFinderHandler, error) {

	return &KubeFinderHandler{
		kubeFinder:       &kubeFinder,
		FinderReconciler: &FinderReconciler,
		stopper:          make(chan struct{}),
	}, nil
}

func (h *KubeFinderHandler) Run() error {

	// do something until h.stopper is closed.
	// if you want to stop this goroutine, close h.stopper.
	for {
		select {
		case <-h.stopper:
			fmt.Printf("stopping Goroutine for KubeFinder: <%s>", h.kubeFinder.Name)
			return nil
		default:
			for _, i := range h.kubeFinder.Spec.Find {
				switch i {
				case "pods":
					err := h.FinderReconciler.handlePods(context.Background(), h.kubeFinder)
					if err != nil {
						h.FinderReconciler.logger.Error(err, "unable to handle pods")
					}
				case "service":
					h.FinderReconciler.logger.Info("Finder Spec: service")
				case "deployment":
					h.FinderReconciler.logger.Info("Finder Spec: deployment")
				}
			}
			time.Sleep(60 * time.Second)
		}
	}

}

func (h *KubeFinderHandler) Stop() {
	close(h.stopper)
}
