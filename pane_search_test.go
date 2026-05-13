package main

import (
	"reflect"
	"testing"
)

func TestForwardSearchOrder(t *testing.T) {
	got := forwardSearchOrder(2, 5, true)
	want := []int{3, 4, 0, 1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwardSearchOrder(2,5,true) = %v want %v", got, want)
	}
	got = forwardSearchOrder(0, 3, false)
	want = []int{0, 1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwardSearchOrder(0,3,false) = %v want %v", got, want)
	}
}

func TestBackwardSearchOrder(t *testing.T) {
	got := backwardSearchOrder(2, 5, true)
	want := []int{1, 0, 4, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backwardSearchOrder(2,5,true) = %v want %v", got, want)
	}
	got = backwardSearchOrder(0, 3, false)
	want = []int{2, 1, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backwardSearchOrder(0,3,false) = %v want %v", got, want)
	}
}

func TestCompileLineMatcher(t *testing.T) {
	m, err := compileLineMatcher("foo", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !m("Foo") || !m("food") || m("bar") {
		t.Fatal("literal case-fold")
	}
	m, err = compileLineMatcher("foo", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if m("Foo") || !m("foo") {
		t.Fatal("literal case-sensitive")
	}
	m, err = compileLineMatcher(`\d+`, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !m("x 99 y") || m("no digits") {
		t.Fatal("regex")
	}
}
