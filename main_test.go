package main

import "testing"

func Test_isProImage(t *testing.T) {
	images := []struct {
		name string
		want bool
	}{
		{"ghcr.io/openfaas/gateway:0.23.2", false},
		{"ghcr.io/openfaasltd/gateway:0.2.0", true},
	}

	for _, image := range images {
		got := isProImage(image.name)
		if got != image.want {
			t.Errorf("Checking: %s returned '%v' while '%v' is expected", image.name, got, image.want)
		}
	}

}
