package caddyx

import (
	"reflect"
	"testing"

	"github.com/hyprsh/tnl/internal/edge/routes"
)

// Each routes.Route field is either part of the transport's key or named here as
// one the transport does not depend on. A field added to Route lands in neither
// until someone decides, and this test fails until they do.
func TestTransportKeyCoversTheRoute(t *testing.T) {
	notInKey := map[string]bool{"HostHeader": true, "NoHSTS": true, "Headers": true}
	rt := reflect.TypeOf(routes.Route{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		var a, b routes.Route
		f := reflect.ValueOf(&b).Elem().Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			f.Set(reflect.MakeSlice(f.Type(), 1, 1))
		default:
			t.Fatalf("routes.Route.%s is a %s: teach this test to change it", name, f.Kind())
		}
		changed := keyOf(a) != keyOf(b)
		switch {
		case notInKey[name] && changed:
			t.Errorf("%s is listed as not in the key, but it is", name)
		case !notInKey[name] && !changed:
			t.Errorf("transportKey does not cover routes.Route.%s", name)
		}
	}
}
