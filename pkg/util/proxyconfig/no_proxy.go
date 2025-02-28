package proxyconfig

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/ghodss/yaml"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-network-operator/pkg/platform"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const defaultCIDR = "0.0.0.0/0"

// MergeUserSystemNoProxy merges user supplied noProxy settings from proxy
// with cluster-wide noProxy settings. It returns a merged, comma-separated
// string of noProxy settings. If no user supplied noProxy settings are
// provided, a comma-separated string of cluster-wide noProxy settings
// are returned.
func MergeUserSystemNoProxy(proxy *configv1.Proxy, infra *configv1.Infrastructure, network *configv1.Network, cluster *corev1.ConfigMap) (string, error) {
	return mergeUserSystemNoProxy(proxy, infra, network, cluster, os.Getenv)
}

func mergeUserSystemNoProxy(proxy *configv1.Proxy, infra *configv1.Infrastructure, network *configv1.Network, cluster *corev1.ConfigMap, getEnv func(string) string) (string, error) {
	// TODO: This will be flexible when master machine management is more dynamic.
	type machineNetworkEntry struct {
		// CIDR is the IP block address pool for machines within the cluster.
		CIDR string `json:"cidr"`
	}
	type installConfig struct {
		ControlPlane struct {
			Replicas string `json:"replicas"`
		} `json:"controlPlane"`
		Networking struct {
			MachineCIDR    string                `json:"machineCIDR"`
			MachineNetwork []machineNetworkEntry `json:"machineNetwork,omitempty"`
		} `json:"networking"`
	}

	var ic installConfig
	data, ok := cluster.Data["install-config"]
	if !ok {
		return "", fmt.Errorf("missing install-config in configmap")
	}
	if err := yaml.Unmarshal([]byte(data), &ic); err != nil {
		return "", fmt.Errorf("invalid install-config: %v\njson:\n%s", err, data)
	}

	set := sets.NewString(
		"127.0.0.1",
		"localhost",
		".svc",
		".cluster.local",
	)
	if hcpCfg := platform.NewHyperShiftConfig(); hcpCfg.Enabled {
		set.Insert(".hypershift.local")
	}

	if ic.Networking.MachineCIDR != "" && ic.Networking.MachineCIDR != defaultCIDR {
		if _, _, err := net.ParseCIDR(ic.Networking.MachineCIDR); err != nil {
			return "", fmt.Errorf("MachineCIDR has an invalid CIDR: %s", ic.Networking.MachineCIDR)
		}
		set.Insert(ic.Networking.MachineCIDR)
	}

	for _, mc := range ic.Networking.MachineNetwork {
		if mc.CIDR == defaultCIDR {
			continue
		}
		if _, _, err := net.ParseCIDR(mc.CIDR); err != nil {
			return "", fmt.Errorf("MachineNetwork has an invalid CIDR: %s", mc.CIDR)
		}
		set.Insert(mc.CIDR)
	}

	// Hypershift does in many but not all cases not actually have an internal apiserver address and just
	// puts the external one in this field (because components expect it to be non-empty). We can not
	// derive from the cluster if we need to proxy the internal apiserver address or not, so we have this
	// knob that allows Hypershift to tell us.
	if getEnv("PROXY_INTERNAL_APISERVER_ADDRESS") != "true" {
		if len(infra.Status.APIServerInternalURL) > 0 {
			internalAPIServer, err := url.Parse(infra.Status.APIServerInternalURL)
			if err != nil {
				return "", fmt.Errorf("failed to parse internal api server internal url")
			}
			set.Insert(internalAPIServer.Hostname())
		} else {
			return "", fmt.Errorf("internal api server url missing from infrastructure config '%s'", infra.Name)
		}
	}

	if len(network.Status.ServiceNetwork) > 0 {
		for _, nss := range network.Status.ServiceNetwork {
			set.Insert(nss)
		}
	} else {
		return "", fmt.Errorf("serviceNetwork missing from network '%s' status", network.Name)
	}

	if infra.Status.PlatformStatus != nil {
		switch infra.Status.PlatformStatus.Type {
		case configv1.AWSPlatformType, configv1.GCPPlatformType, configv1.AzurePlatformType, configv1.OpenStackPlatformType:
			set.Insert("169.254.169.254")
		}

		// Construct the node sub domain.
		// TODO: Add support for additional cloud providers.
		switch infra.Status.PlatformStatus.Type {
		case configv1.AWSPlatformType:
			region := infra.Status.PlatformStatus.AWS.Region
			if region == "us-east-1" {
				set.Insert(".ec2.internal")
			} else {
				set.Insert(fmt.Sprintf(".%s.compute.internal", region))
			}
		case configv1.AzurePlatformType:
			if cloudName := infra.Status.PlatformStatus.Azure.CloudName; cloudName != configv1.AzurePublicCloud {
				// https://learn.microsoft.com/en-us/azure/virtual-network/what-is-ip-address-168-63-129-16
				set.Insert("168.63.129.16")
				// https://bugzilla.redhat.com/show_bug.cgi?id=2104997
				if cloudName == configv1.AzureStackCloud {
					set.Insert(infra.Status.PlatformStatus.Azure.ARMEndpoint)
				}
			}
		case configv1.GCPPlatformType:
			// From https://cloud.google.com/vpc/docs/special-configurations add GCP metadata.
			// "metadata.google.internal." added due to https://bugzilla.redhat.com/show_bug.cgi?id=1754049
			set.Insert("metadata", "metadata.google.internal", "metadata.google.internal.")
		}
	}

	if len(network.Status.ClusterNetwork) > 0 {
		for _, clusterNetwork := range network.Status.ClusterNetwork {
			set.Insert(clusterNetwork.CIDR)
		}
	} else {
		return "", fmt.Errorf("clusterNetwork missing from network `%s` status", network.Name)
	}

	if len(proxy.Spec.NoProxy) > 0 {
		for _, userValue := range strings.Split(proxy.Spec.NoProxy, ",") {
			if userValue != "" {
				set.Insert(userValue)
			}
		}
	}

	return strings.Join(set.List(), ","), nil
}
