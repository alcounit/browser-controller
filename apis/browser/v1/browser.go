package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Browser",type="string",JSONPath=".spec.browserName"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.browserVersion"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="PodIP",type="string",JSONPath=".status.podIP"
// +kubebuilder:printcolumn:name="StartTime",type="date",JSONPath=".status.startTime"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:resource:path=browsers,scope=Namespaced,shortName=brw
// +kubebuilder:storageversion
// +kubebuilder:categories=selenosis
// Browser is the Schema for the browsers API
type Browser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BrowserSpec   `json:"spec,omitempty"`
	Status BrowserStatus `json:"status,omitempty"`
}

// BrowserSpec defines the desired state of BrowserPod
type BrowserSpec struct {
	// BrowserName specifies the name of the browser to use (e.g., chrome, firefox)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	BrowserName string `json:"browserName"`

	// BrowserVersion specifies the version of the browser to use (e.g., 91.0, 88.0)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	BrowserVersion string `json:"browserVersion"`
}

// BrowserStatus defines the observed state of BrowserPod
type BrowserStatus struct {
	// PodIP is the IP address allocated to the pod
	// +optional
	PodIP string `json:"podIP,omitempty"`

	// Phase is the current lifecycle phase of the pod
	// +optional
	Phase corev1.PodPhase `json:"phase,omitempty"`

	// A human readable message indicating details about why the pod is in this condition.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,3,opt,name=message"`

	// A brief CamelCase message indicating details about why the pod is in this state.
	// e.g. 'Evicted'
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`

	// StartTime is when the pod was started
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// ContainerStatuses provides detailed status information about each container
	// +optional
	// +listType=atomic
	ContainerStatuses []ContainerStatus `json:"containerStatuses,omitempty"`
}

// ContainerStatus represents the status of a container
type ContainerStatus struct {
	// Name of the container
	Name string `json:"name"`

	// State holds details about the container's current condition
	// +optional
	State corev1.ContainerState `json:"state,omitempty"`

	// Image is the image the container is running
	// +optional
	Image string `json:"image,omitempty"`

	// RestartCount is the number of times the container has been restarted
	RestartCount int32 `json:"restartCount"`

	// Ports exposed by the container
	// +optional
	// +listType=atomic
	Ports []ContainerPort `json:"ports,omitempty"`
}

// ContainerPort represents a network port in a container
type ContainerPort struct {
	// Name for the port that can be referred to by services
	// +optional
	Name string `json:"name,omitempty"`

	// Number of port to expose on the container
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort"`

	// Protocol for port. Must be UDP, TCP, or SCTP
	// +optional
	Protocol corev1.Protocol `json:"protocol,omitempty"`

	// Number of port to expose on the host
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	HostPort int32 `json:"hostPort,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// BrowserList contains a list of BrowserPod
type BrowserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Browser `json:"items"`
}
