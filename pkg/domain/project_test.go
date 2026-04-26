package domain

import (
	"reflect"
	"testing"
)

func TestProjectDoesNotExposeSetupCommands(t *testing.T) {
	if _, ok := reflect.TypeOf(Project{}).FieldByName("SetupCommands"); ok {
		t.Fatal("Project should not expose setup commands")
	}
	if _, ok := reflect.TypeOf(NewProjectInput{}).FieldByName("SetupCommands"); ok {
		t.Fatal("NewProjectInput should not accept setup commands")
	}
}
