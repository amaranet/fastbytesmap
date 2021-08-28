package fastintmap

import (
	"github.com/itsabgr/fastintmap/pkg/sortedlist"
)

// Get retrieves an element from the map under given hashed key.
func (m *Map) Get(key uintptr) (value interface{}, ok bool) {
	data, element := m.indexElement(key)
	if data == nil {
		return nil, false
	}

	// inline Map.searchItem()
	for element != nil {
		if element.Key() == key {
			return element.Value(), true
		}

		if element.Key() > key {
			return nil, false
		}

		element = element.Next()
	}
	return nil, false
}

// GetOrAdd returns the existing value for the key if present.
// Otherwise, it stores and returns the given value.
// The loaded result is true if the value was loaded, false if stored.
func (m *Map) GetOrAdd(key uintptr, value interface{}) (actual interface{}, loaded bool) {
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
					actual = element.Value()
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
