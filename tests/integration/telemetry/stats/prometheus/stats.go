// +build integ
// Copyright Istio Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package prometheus

import (
	"context"
	"fmt"
	"testing"

	"golang.org/x/sync/errgroup"

	"istio.io/istio/pkg/config/protocol"
	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/echo"
	"istio.io/istio/pkg/test/framework/components/echo/echoboot"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/components/istio/ingress"
	"istio.io/istio/pkg/test/framework/components/namespace"
	"istio.io/istio/pkg/test/framework/components/prometheus"
	"istio.io/istio/pkg/test/framework/components/telemetry"
	"istio.io/istio/pkg/test/framework/features"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/util/retry"
	util "istio.io/istio/tests/integration/telemetry"
)

var (
	client, server echo.Instances
	ist            istio.Instance
	appNsInst      namespace.Instance
	promInst       prometheus.Instance
	ingr           []ingress.Instance
)

// GetIstioInstance gets Istio instance.
func GetIstioInstance() *istio.Instance {
	return &ist
}

// GetAppNamespace gets bookinfo instance.
func GetAppNamespace() namespace.Instance {
	return appNsInst
}

// GetPromInstance gets prometheus instance.
func GetPromInstance() prometheus.Instance {
	return promInst
}

// GetIstioInstance gets Istio instance.
func GetIngressInstance() []ingress.Instance {
	return ingr
}

func GetClientInstances() echo.Instances {
	return client
}

func GetServerInstances() echo.Instances {
	return server
}

// TestStatsFilter includes common test logic for stats and mx exchange filters running
// with nullvm and wasm runtime.
func TestStatsFilter(t *testing.T, feature features.Feature) {
	framework.NewTest(t).
		Features(feature).
		Run(func(ctx framework.TestContext) {
			sourceQuery, destinationQuery, appQuery := buildQuery()

			g, _ := errgroup.WithContext(context.Background())
			for _, cltInstance := range client {
				cltInstance := cltInstance
				g.Go(func() error {
					err := retry.UntilSuccess(func() error {
						if err := SendTraffic(t, cltInstance); err != nil {
							return err
						}
						c := cltInstance.Config().Cluster
						// Query client side metrics
						if _, err := QueryPrometheus(t, c, sourceQuery, GetPromInstance()); err != nil {
							t.Logf("prometheus values for istio_requests_total for cluster %v: \n%s", c, util.PromDump(c, promInst, "istio_requests_total"))
							return err
						}
						if _, err := QueryPrometheus(t, c, destinationQuery, GetPromInstance()); err != nil {
							t.Logf("prometheus values for istio_requests_total for cluster %v: \n%s", c, util.PromDump(c, promInst, "istio_requests_total"))
							return err
						}
						// This query will continue to increase due to readiness probe; don't wait for it to converge
						if err := QueryFirstPrometheus(t, c, appQuery, GetPromInstance()); err != nil {
							t.Logf("prometheus values for istio_echo_http_requests_total for cluster %v: \n%s", c, util.PromDump(c, promInst, "istio_echo_http_requests_total"))
							return err
						}

						return nil
					}, retry.Delay(telemetry.RetryDelay), retry.Timeout(telemetry.RetryTimeout))
					if err != nil {
						return err
					}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				t.Fatalf("test failed: %v", err)
			}
		})
}

// TestStatsTCPFilter includes common test logic for stats and mx exchange filters running
// with nullvm and wasm runtime for TCP.
func TestStatsTCPFilter(t *testing.T, feature features.Feature) {
	framework.NewTest(t).
		Features(feature).
		Run(func(ctx framework.TestContext) {
			destinationQuery := buildTCPQuery()

			g, _ := errgroup.WithContext(context.Background())
			for _, cltInstance := range client {
				cltInstance := cltInstance
				g.Go(func() error {
					err := retry.UntilSuccess(func() error {
						if err := SendTCPTraffic(t, cltInstance); err != nil {
							return err
						}
						c := cltInstance.Config().Cluster
						if _, err := QueryPrometheus(t, c, destinationQuery, GetPromInstance()); err != nil {
							t.Logf("prometheus values for istio_tcp_connections_opened_total: \n%s", util.PromDump(c, promInst, "istio_tcp_connections_opened_total"))
							return err
						}

						return nil
					}, retry.Delay(telemetry.RetryDelay), retry.Timeout(telemetry.RetryTimeout))
					if err != nil {
						return err
					}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				t.Fatalf("test failed: %v", err)
			}
		})
}

// TestSetup set up echo app for stats testing.
func TestSetup(ctx resource.Context) (err error) {
	appNsInst, err = namespace.New(ctx, namespace.Config{
		Prefix: "echo",
		Inject: true,
	})
	if err != nil {
		return
	}

	echos, err := echoboot.NewBuilder(ctx).
		WithClusters(ctx.Clusters()...).
		With(nil, echo.Config{
			Service:   "client",
			Namespace: appNsInst,
			Ports:     nil,
			Subsets:   []echo.SubsetConfig{{}},
		}).
		With(nil, echo.Config{
			Service:   "server",
			Namespace: appNsInst,
			Subsets:   []echo.SubsetConfig{{}},
			Ports: []echo.Port{
				{
					Name:         "http",
					Protocol:     protocol.HTTP,
					InstancePort: 8090,
				},
				{
					Name:     "tcp",
					Protocol: protocol.TCP,
					// We use a port > 1024 to not require root
					InstancePort: 9000,
				},
			},
		}).Build()
	if err != nil {
		return err
	}
	for _, c := range ctx.Clusters() {
		ingr = append(ingr, ist.IngressFor(c))
	}
	client = echos.Match(echo.Service("client"))
	server = echos.Match(echo.Service("server"))
	promInst, err = prometheus.New(ctx, prometheus.Config{})
	if err != nil {
		return
	}
	return nil
}

// SendTraffic makes a client call to the "server" service on the http port.
func SendTraffic(t *testing.T, cltInstance echo.Instance) error {
	_, err := cltInstance.Call(echo.CallOptions{
		Target:   server[0],
		PortName: "http",
		Count:    util.RequestCountMultipler * len(server),
	})
	if err != nil {
		return err
	}
	return nil
}

// SendTCPTraffic makes a client call to the "server" service on the tcp port.
func SendTCPTraffic(t *testing.T, cltInstance echo.Instance) error {
	_, err := cltInstance.Call(echo.CallOptions{
		Target:   server[0],
		PortName: "tcp",
		Count:    util.RequestCountMultipler * len(server),
	})
	if err != nil {
		return err
	}
	return nil
}

// BuildQueryCommon is the shared function to construct prom query for istio_request_total metric.
func BuildQueryCommon(labels map[string]string, ns string) (sourceQuery, destinationQuery, appQuery string) {
	sourceQuery = `istio_requests_total{reporter="source",`
	destinationQuery = `istio_requests_total{reporter="destination",`

	for k, v := range labels {
		sourceQuery += fmt.Sprintf(`%s=%q,`, k, v)
		destinationQuery += fmt.Sprintf(`%s=%q,`, k, v)
	}
	sourceQuery += "}"
	destinationQuery += "}"
	appQuery += `istio_echo_http_requests_total{kubernetes_namespace="` + ns + `"}`
	return
}

func buildQuery() (sourceQuery, destinationQuery, appQuery string) {
	ns := GetAppNamespace()
	labels := map[string]string{
		"request_protocol":               "http",
		"response_code":                  "200",
		"destination_app":                "server",
		"destination_version":            "v1",
		"destination_service":            "server." + ns.Name() + ".svc.cluster.local",
		"destination_service_name":       "server",
		"destination_workload_namespace": ns.Name(),
		"destination_service_namespace":  ns.Name(),
		"source_app":                     "client",
		"source_version":                 "v1",
		"source_workload":                "client-v1",
		"source_workload_namespace":      ns.Name(),
	}

	return BuildQueryCommon(labels, ns.Name())
}

func buildTCPQuery() (destinationQuery string) {
	ns := GetAppNamespace()
	destinationQuery = `istio_tcp_connections_opened_total{reporter="destination",`
	labels := map[string]string{
		"request_protocol":               "tcp",
		"destination_service_name":       "server",
		"destination_canonical_revision": "v1",
		"destination_canonical_service":  "server",
		"destination_app":                "server",
		"destination_version":            "v1",
		"destination_workload_namespace": ns.Name(),
		"destination_service_namespace":  ns.Name(),
		"source_app":                     "client",
		"source_version":                 "v1",
		"source_workload":                "client-v1",
		"source_workload_namespace":      ns.Name(),
	}
	for k, v := range labels {
		destinationQuery += fmt.Sprintf(`%s=%q,`, k, v)
	}
	destinationQuery += "}"
	return
}
