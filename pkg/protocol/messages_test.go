package protocol

import (
	"reflect"
	"testing"
)

func TestProjectPayloadDoesNotExposeSetupCommands(t *testing.T) {
	if _, ok := reflect.TypeOf(ProjectPayload{}).FieldByName("SetupCommands"); ok {
		t.Fatal("ProjectPayload should not expose setup commands")
	}
}
