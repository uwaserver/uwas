package database

import (
	"fmt"
	"testing"
)

func databaseWhitespaceList(out string) (dbs []DBInfo, err error, pan any) {
	old := runMySQLFn
	defer func() { runMySQLFn = old; pan = recover() }()
	runMySQLFn = func(string) (string, error) { return out, nil }
	dbs, err = ListDatabases()
	return
}
func TestListDatabasesWhitespaceBetweenRows(t *testing.T) {
	dbs, err, pan := databaseWhitespaceList("first\t1.00\t3\nsecond\t2.00\t4\n")
	if pan != nil || err != nil || len(dbs) != 2 {
		t.Fatalf("CONTROL FAILED %v %v %v", dbs, err, pan)
	}
	fmt.Println("CONTROL PASSED")
	dbs, err, pan = databaseWhitespaceList("first\t1.00\t3\n   \t  \nsecond\t2.00\t4\n")
	fmt.Printf("EXPECTED: 2 databases, no panic ACTUAL: %d databases, error=%v panic=%v\n", len(dbs), err, pan)
	if pan != nil || err != nil || len(dbs) != 2 {
		fmt.Println("PROBLEM CONFIRMED")
		t.FailNow()
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
	for _, tc := range []struct {
		out   string
		count int
	}{{"", 0}, {" \t \n \t ", 0}, {"SCHEMA_NAME\tsize\ttables\n\t \nfirst\t1.00\t3\n\t \n", 1}, {"first\n \t \nsecond\n\t \nthird\n", 3}} {
		dbs, err, pan := databaseWhitespaceList(tc.out)
		if err != nil || pan != nil || len(dbs) != tc.count {
			t.Fatalf("edge %q: dbs=%v err=%v panic=%v", tc.out, dbs, err, pan)
		}
	}
	fmt.Println("FIX VERIFIED")

}
