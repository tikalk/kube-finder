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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type slackProperties struct {
	ChannelID string `json:"channelID,omitempty"`
}

type Notify struct {
	Slack slackProperties `json:"slack,omitempty"`
}

// FinderSpec defines the desired state of Finder
type FinderSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Foo is an example field of Finder. Edit finder_types.go to remove/update
	Find   []string `json:"find,omitempty"`
	Notify Notify   `json:"notify,omitempty"`
}

// FinderStatus defines the observed state of Finder
type FinderStatus struct {
	FoundPods map[string]FoundSpec `json:"foundPods,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Finder is the Schema for the finders API
type Finder struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FinderSpec   `json:"spec,omitempty"`
	Status FinderStatus `json:"status,omitempty"`
}

// adding foundSpec struct
type FoundSpec struct {
	Name       string   `json:"name,omitempty"`
	Namespace  string   `json:"namespace,omitempty"`
	ObjectType string   `json:"objectType,omitempty"`
	Message    string   `json:"message,omitempty"`
	Events     []string `json:"events,omitempty"`
}

//+kubebuilder:object:root=true

// FinderList contains a list of Finder
type FinderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Finder `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Finder{}, &FinderList{})
}
