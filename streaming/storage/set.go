package storage

//TODO: this code is of questionable quality. It might be useful to change it later

import (
	"github.com/cube2222/octosql"
	"github.com/dgraph-io/badger/v2"
	"github.com/golang/protobuf/proto"
	"github.com/mitchellh/hashstructure"
	"github.com/pkg/errors"
)

type Set struct {
	tx StateTransaction
}

type SetIterator struct {
	it               Iterator
	currentHashTuple []octosql.Value
	currentCounter   int
}

func NewSet(tx StateTransaction) *Set {
	return &Set{
		tx: tx,
	}
}

func (set *Set) Insert(value octosql.Value) (bool, error) {
	return set.insertUsingHash(value, hashValue)
}

func (set *Set) insertUsingHash(value octosql.Value, hashFunction func(octosql.Value) ([]byte, error)) (bool, error) {
	hash, err := hashFunction(value)
	if err != nil {
		return false, errors.Wrap(err, "couldn't parse given value in insert")
	}

	return set.insertValueWithGivenHash(value, hash)
}

func (set *Set) Contains(value octosql.Value) (bool, error) {
	return set.containsUsingHash(value, hashValue)
}

func (set *Set) containsUsingHash(value octosql.Value, hashFunction func(octosql.Value) ([]byte, error)) (bool, error) {
	hash, err := hashFunction(value)
	if err != nil {
		return false, errors.Wrap(err, "couldn't parse given value in contains")
	}

	hashedTxn := set.tx.WithPrefix(hash)
	state := NewValueState(hashedTxn)

	var tuple octosql.Value
	err = state.Get(&tuple)

	if err == ErrKeyNotFound {
		return false, nil
	} else if err != nil {
		return false, errors.Wrap(err, "failed to read set")
	}

	values := tuple.AsSlice()
	return getPositionInTuple(values, value) != -1, nil
}

func (set *Set) Erase(value octosql.Value) (bool, error) {
	return set.eraseUsingHash(value, hashValue)
}

func (set *Set) eraseUsingHash(value octosql.Value, hashFunction func(octosql.Value) ([]byte, error)) (bool, error) {
	hash, err := hashFunction(value)
	if err != nil {
		return false, errors.Wrap(err, "couldn't parse given value in erase")
	}

	hashedTxn := set.tx.WithPrefix(hash)
	state := NewValueState(hashedTxn)

	var tuple octosql.Value
	err = state.Get(&tuple)

	if err == ErrKeyNotFound {
		return false, nil //the element wasn't present in the set
	} else if err != nil {
		return false, errors.Wrap(err, "failed to read set elements")
	}

	tupleSlice := tuple.AsSlice()
	index := getPositionInTuple(tupleSlice, value)

	if index == -1 {
		return false, nil
	}

	newTuple := removeElementFromTuple(index, tupleSlice)

	//if the removed value is last of it's hash we clear it
	if len(newTuple.AsSlice()) == 0 {
		err := set.tx.Delete(hash)
		if err != nil {
			return false, errors.Wrap(err, "couldn't clear set value")
		}

		return true, nil
	}

	err = state.Set(&newTuple)
	if err != nil {
		return false, errors.Wrap(err, "failed to actualize value of set")
	}

	return true, nil
}

func (set *Set) GetIterator() *SetIterator {
	options := badger.DefaultIteratorOptions
	it := set.tx.Iterator(options)
	it.Rewind()

	return NewSetIterator(it)
}

func NewSetIterator(it Iterator) *SetIterator {
	return &SetIterator{
		it:               it,
		currentHashTuple: []octosql.Value{},
		currentCounter:   0,
	}
}

func (si *SetIterator) Next(value *octosql.Value) error {
	var tuple octosql.Value

	if si.currentCounter == len(si.currentHashTuple) {
		err := si.it.Next(&tuple)
		if err == ErrEndOfIterator {
			return ErrEndOfIterator
		} else if err != nil {
			return errors.Wrap(err, "failed to read next tuple from underlying iterator")
		}

		si.currentHashTuple = tuple.AsSlice()
		si.currentCounter = 0
	}

	*value = si.currentHashTuple[si.currentCounter]
	si.currentCounter++

	return nil
}

func (si *SetIterator) Close() error {
	return si.it.Close()
}

func (si *SetIterator) Rewind() {
	si.it.Rewind()
}

// Auxiliary functions
func hashValue(value octosql.Value) ([]byte, error) {
	valueBytes, err := proto.Marshal(&value)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't marshal value")
	}

	hashValue, err := hashstructure.Hash(valueBytes, nil)
	if err != nil {
		return nil, errors.Wrap(err, "couldn't hash value")
	}

	hashKey := octosql.MonotonicMarshalUint64(hashValue, true)

	return hashKey, nil
}

//Returns the position of a value in a tuple if it's a member of the tuple,
//and -1 if it's not.
func getPositionInTuple(values []octosql.Value, value octosql.Value) int {
	for i, v := range values {
		if octosql.AreEqual(v, value) {
			return i
		}
	}

	return -1
}

//This is an auxiliary function used in Insert. It also serves as a testing
//utility, to see if the set works properly when multiple values map to the same hash.
func (set *Set) insertValueWithGivenHash(value octosql.Value, hash []byte) (bool, error) {
	hashedTxn := set.tx.WithPrefix(hash)
	state := NewValueState(hashedTxn)

	var tuple octosql.Value
	err := state.Get(&tuple)

	if err == ErrKeyNotFound {
		tuple = octosql.ZeroTuple()
	} else if err != nil {
		return false, errors.Wrap(err, "failed to read set elements")
	}

	tupleSlice := tuple.AsSlice()

	if getPositionInTuple(tupleSlice, value) != -1 {
		return false, nil
	}

	newTuple := addValueToTuple(value, tupleSlice)

	err = state.Set(&newTuple)
	if err != nil {
		return false, errors.Wrap(err, "couldn't actualize new value for set")
	}

	return true, nil
}

func removeElementFromTuple(index int, values []octosql.Value) octosql.Value {
	length := len(values)
	result := values[:index]

	if index != length-1 {
		result = append(result, values[index+1:]...)
	}

	return octosql.MakeTuple(result)
}

func addValueToTuple(value octosql.Value, tuple []octosql.Value) octosql.Value {
	return octosql.MakeTuple(append(tuple, value))
}
