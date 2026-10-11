package main

import (
	"reflect"
	"testing"

	"github.com/uug-ai/exercise-workflows/internal/app"
)

func TestExecutablePackageLinksInternalApp(t *testing.T) {
	if got := reflect.TypeOf(app.Run).String(); got != "func() int" {
		t.Fatalf("app.Run signature = %s", got)
	}
}
