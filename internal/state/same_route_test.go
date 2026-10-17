package state

import (
	"reflect"
	"testing"
)

// A field added to Route that sameRoute does not compare would make an edit of
// that field look like no change. Each field is changed in turn.
func TestSameRouteComparesEveryField(t *testing.T) {
	base := reflect.ValueOf(Route{}).Type()
	for i := 0; i < base.NumField(); i++ {
		a, b := reflect.New(base).Elem(), reflect.New(base).Elem()
		f := b.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			f.Set(reflect.MakeSlice(f.Type(), 1, 1))
		default:
			t.Fatalf("Route.%s is a %s: teach this test to change it", base.Field(i).Name, f.Kind())
		}
		if sameRoute(a.Interface().(Route), b.Interface().(Route)) {
			t.Errorf("sameRoute does not compare Route.%s", base.Field(i).Name)
		}
	}
}
