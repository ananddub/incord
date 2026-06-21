package util

import "reflect"

func SimilarValuesCopy[T any, R any](src T, dst R) *R {
	sv := reflect.ValueOf(src)
	dv := reflect.ValueOf(&dst).Elem()

	if sv.Kind() == reflect.Pointer {
		sv = sv.Elem()
	}

	for i := 0; i < dv.NumField(); i++ {
		df := dv.Type().Field(i)

		sf := sv.FieldByName(df.Name)
		if !sf.IsValid() {
			continue
		}

		if sf.Type() != dv.Field(i).Type() {
			continue
		}

		if dv.Field(i).CanSet() {
			dv.Field(i).Set(sf)
		}
	}

	return &dst
}
