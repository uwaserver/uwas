package database

import (
	"strings"
	"testing"
)

// `docker ps --filter name=uwas-db-` is a substring/regex match, so it also
// returns containers UWAS does not manage ("my-uwas-db-prod"). The dashboard
// derives the managed name with name.replace('uwas-db-', ”), which for such a
// container targets a different container ("uwas-db-my-prod") on start/stop/rm.
// ListDockerDBs must only report containers that carry the managed prefix, and
// must report the host port the container publishes.
func TestListDockerDBsOnlyManagedWithPort(t *testing.T) {
	saveDockerHook(t)
	out := strings.Join([]string{
		`{"ID":"a1","Names":"uwas-db-shop","Image":"mariadb:11","Status":"Up","State":"running","Ports":"127.0.0.1:3307->3306/tcp"}`,
		`{"ID":"b2","Names":"my-uwas-db-prod","Image":"mysql:8","Status":"Up","State":"running","Ports":"0.0.0.0:3400->3306/tcp"}`,
		`{"ID":"c3","Names":"uwas-db-pg","Image":"postgres:16","Status":"Exited","State":"exited","Ports":""}`,
		`{"ID":"d4","Names":"uwas-db-multi","Image":"mariadb:11","Status":"Up","State":"running","Ports":"127.0.0.1:3310->3306/tcp, [::1]:3310->3306/tcp"}`,
	}, "\n")
	dockerExecCommandFn = fakeDockerCmd(out, 0)

	got, err := ListDockerDBs()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	ports := map[string]int{}
	for _, c := range got {
		names = append(names, c.Name)
		ports[c.Name] = c.Port
	}
	if strings.Join(names, ",") != "uwas-db-shop,uwas-db-pg,uwas-db-multi" {
		t.Errorf("EXPECTED managed containers only; ACTUAL %v", names)
	}
	if ports["uwas-db-shop"] != 3307 || ports["uwas-db-multi"] != 3310 || ports["uwas-db-pg"] != 0 {
		t.Errorf("EXPECTED ports shop=3307 multi=3310 pg=0 (stopped); ACTUAL %v", ports)
	}
}
