// fakeapi runs a stand-in Kubernetes API server so you can try Thrum's
// live mode without a cluster:
//
//	go run ./hack/fakeapi --scenario failing &
//	go run ./cmd/thrum-collector --kubeconfig /tmp/thrum-fake.kubeconfig
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/yeixio/thrum/collector/internal/fakeapi"
)

func main() {
	scenario := flag.String("scenario", "healthy", "healthy, busy or failing")
	listen := flag.String("listen", "127.0.0.1:7879", "address to serve on")
	out := flag.String("kubeconfig", "/tmp/thrum-fake.kubeconfig", "where to write a kubeconfig for the collector")
	flag.Parse()

	cluster, err := fakeapi.Scenario(*scenario)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, []byte(fakeapi.Kubeconfig("http://"+*listen, "fake-"+*scenario)), 0o600); err != nil {
		log.Fatal(err)
	}
	log.Printf("fake %s cluster on http://%s, kubeconfig written to %s", *scenario, *listen, *out)
	log.Fatal(http.ListenAndServe(*listen, fakeapi.Handler(cluster)))
}
