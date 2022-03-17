package fastintmap

import (
	"fmt"
	"github.com/itsabgr/fastintmap/pkg/sortedlist"
)

func cast[T any](t interface{}) T {
	switch t.(type) {
	case T:
		return t.(T)
	}
	panic(fmt.Errorf("unsupported type %T", t))
}

// Get retrieves an element from the map under given hashed key.
func (m *Map[T]) Get(key uintptr) (value T, ok bool) {
	data, element := m.indexElement(key)
	if data == nil {
		return value, false
	}

	// inline Map.searchItem()
	for element != nil {
		if element.Key() == key {
			return cast[T](element.Value()), true
		}

		if element.Key() > key {
			return value, false
		}

		element = element.Next()
	}
	return value, false
}

// GetOrAdd returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
func (m *Map[T]) GetOrAdd(key uintptr, value T) (actual T, loaded bool) {
	h := key
	var newElement *sortedlist.ListElement

	for {
		data, element := m.indexElement(h)
		if data == nil {
			m.allocate(DefaultSize)
			continue
		}

		for element != nil {
			if element.Key() == h {

				if element.Key() == key {
					actual = cast[T](element.Value())
					return actual, true

				}
			}

			if element.Key() > h {
				break
			}

			element = element.Next()
		}

		if newElement == nil { // allocate only once
			newElement = sortedlist.NewElement(key, value)
		}

		if m.insertListElement(newElement, false) {
			return value, false
		}
	}
}
