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

package client

import (
	"errors"
	"fmt"
	"testing"
	"time"

	uuid "github.com/satori/go.uuid"

	"istio.io/istio/pkg/test/framework"
	"istio.io/istio/pkg/test/framework/components/istio"
	"istio.io/istio/pkg/test/framework/label"
	"istio.io/istio/pkg/test/framework/resource"
	"istio.io/istio/pkg/test/util/retry"
	"istio.io/istio/tests/integration/telemetry/tracing"
)

// TestClientTracing exercises the trace generation features of Istio, based on the Envoy Trace driver for zipkin using
// client initiated tracing using envoy traceheader.
// The test verifies that all expected spans (a client span and a server span for each service call in the sample bookinfo app)
// are generated and that they are all a part of the same distributed trace with correct hierarchy and name.
func TestClientTracing(t *testing.T) {
	framework.NewTest(t).
		Features("observability.telemetry.tracing.client").
		Run(func(ctx framework.TestContext) {
			appNsInst := tracing.GetAppNamespace()

			for _, cl := range ctx.Clusters() {
				clName := cl.Name()
				t.Run(clName, func(t *testing.T) {
					if cl.NetworkName() != ctx.Clusters().Default().NetworkName() {
						t.Skip("tracing fails on cross-network client; see https://github.com/istio/istio/issues/28890")
					}
					t.Logf("Verifying for cluster %s", clName)
					retry.UntilSuccessOrFail(t, func() error {
						// Send test traffic with a trace header.
						id := uuid.NewV4().String()
						extraHeader := map[string][]string{
							tracing.TraceHeader: {id},
						}
						err := tracing.SendTraffic(t, extraHeader, cl)
						if err != nil {
							return fmt.Errorf("cannot send traffic from cluster %s: %v", clName, err)
						}
						traces, err := tracing.GetZipkinInstance().QueryTraces(100,
							fmt.Sprintf("server.%s.svc.cluster.local:80/*", appNsInst.Name()), "")
						if err != nil {
							return fmt.Errorf("cannot get traces from zipkin: %v", err)
						}
						if !tracing.VerifyEchoTraces(t, appNsInst.Name(), clName, traces) {
							return errors.New("cannot find expected traces")
						}
						return nil
					}, retry.Delay(3*time.Second), retry.Timeout(80*time.Second))
				})
			}
		})
}

func TestMain(m *testing.M) {
	framework.NewSuite(m).
		Label(label.CustomSetup).
		Setup(istio.Setup(tracing.GetIstioInstance(), setupConfig)).
		Setup(tracing.TestSetup).
		Run()
}

func setupConfig(ctx resource.Context, cfg *istio.Config) {
	if cfg == nil {
		return
	}
	cfg.Values["meshConfig.enableTracing"] = "true"
}
