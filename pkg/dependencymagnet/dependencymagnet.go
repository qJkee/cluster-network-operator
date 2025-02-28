//go:build tools
// +build tools

// go mod won't pull in code that isn't depended upon, but we have some code we don't depend on from code that must be included
// for our build to work.
package dependencymagnet

import (
	_ "github.com/go-bindata/go-bindata"
	_ "github.com/go-bindata/go-bindata/go-bindata"
	_ "github.com/openshift/build-machinery-go"
	_ "k8s.io/code-generator"

	_ "github.com/openshift/hypershift/api/v1beta1"
)
