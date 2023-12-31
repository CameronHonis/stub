package stub

import (
	"fmt"
	"reflect"
	"sync"
)

type MockedI interface {
	Stub(methodName string, fn interface{})
	IsStubbed(methodName string) bool

	Unstub(methodName string)

	AllCallArgs(methodName string) [][]interface{}
	CallArgs(methodName string, idx int) []interface{}
	LastCallArgs(methodName string) []interface{}
	MethodCallCount(methodName string) int
	WasMethodCalledWith(methodName string, args ...interface{}) bool

	Call(methodName string, args ...interface{}) []interface{}
}

//	Mocked is a struct that provides concrete implementations
//	for the methods of the MockedI interface. This struct is
//	intended to be embedded in a stub struct "wrapper".
//
//	The final hierarchy should look like this:
//		StubWrapper -> Mocked -> StubbedStruct;
//		[where each struct embeds the next]
//
//	I realize that creating and formatting a wrapper to implement
//	this struct may be a painful and delicate process, so I (plan on)
//	providing a generator that will create the wrapper and the stubbed
//	methods for you. The generator would be run as a separate step in
//	the build process.
//
//	* See (TODO - insert file name of example) for an example of usage.
//
//	* See (TODO - insert file name of generated file) for the generated file.
//
//	* See (TODO - insert file name of generator here) for the generator source code.
type Mocked[SO any] struct {
	wrapper              interface{} // the struct that wraps the stubbed object
	stubbedObj           *SO         // the struct being stubbed
	fnByMethodName       map[string]interface{}
	callArgsByMethodName map[string][][]interface{}
	mu                   sync.Mutex // just in case tests run in parallel (is this overkill?)
}

func NewMocked[SO any](wrapper interface{}, objToStub *SO) *Mocked[SO] {
	return &Mocked[SO]{
		wrapper:              wrapper,
		stubbedObj:           objToStub,
		fnByMethodName:       make(map[string]interface{}),
		callArgsByMethodName: make(map[string][][]interface{}),
	}
}

//	NOTE: Thoroughly assert on the typing of the passed fn.
//	This frees us from having to do any type assertions
//	at method invocation time.
//	TODO: add receiver as fn's first param, so I don't have to
//	TODO: do this manually everytime I define a new stub
func (s *Mocked[SO]) Stub(methodName string, fn interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ValidateStubSignature(s.stubbedObj, methodName, fn)
	s.fnByMethodName[methodName] = fn
}

func (s *Mocked[SO]) IsStubbed(methodName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.fnByMethodName[methodName]
	return ok
}

func (s *Mocked[SO]) Unstub(methodName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fnByMethodName, methodName)
}

func (s *Mocked[SO]) AllCallArgs(methodName string) [][]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callArgsByMethodName[methodName] == nil {
		return make([][]interface{}, 0)
	}
	return s.callArgsByMethodName[methodName]
}
func (s *Mocked[SO]) CallArgs(methodName string, idx int) []interface{} {
	allCallArgs := s.AllCallArgs(methodName)
	if len(allCallArgs) <= idx {
		panic(fmt.Sprintf("no call at index %d", idx))
	}
	return allCallArgs[idx]
}

func (s *Mocked[SO]) LastCallArgs(methodName string) []interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callArgsByMethodName[methodName] == nil {
		panic(fmt.Sprintf("no calls to %s were made", methodName))
	}
	return s.callArgsByMethodName[methodName][len(s.callArgsByMethodName[methodName])-1]
}

func (s *Mocked[SO]) MethodCallCount(methodName string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callArgsByMethodName[methodName] == nil {
		return 0
	}
	return len(s.callArgsByMethodName[methodName])
}

func (s *Mocked[SO]) WasMethodCalledWith(methodName string, args ...interface{}) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, callArgs := range s.AllCallArgs(methodName) {
		if reflect.DeepEqual(callArgs, args) {
			return true
		}
	}
	return false
}

func (s *Mocked[SO]) Call(methodName string, args ...interface{}) []interface{} {
	s.mu.Lock()
	fn := s.fnByMethodName[methodName]
	s.mu.Unlock()

	inVals := make([]reflect.Value, len(args)+1)
	inVals[0] = reflect.ValueOf(s.stubbedObj)
	for i, arg := range args {
		inVals[i+1] = reflect.ValueOf(arg)
	}

	if fn == nil {
		sMethod, methodFound := reflect.TypeOf(s.stubbedObj).MethodByName(methodName)
		if !methodFound {
			panic(fmt.Sprintf("stubbed object does not have a method named %s", methodName))
		}
		fn = sMethod.Func.Interface()
	}
	fnVal := reflect.ValueOf(fn)
	outVals := fnVal.Call(inVals)
	out := make([]interface{}, len(outVals))
	for i, outVal := range outVals {
		out[i] = outVal.Interface()
	}
	s.addCallArgs(methodName, args...)
	return out
}

func (s *Mocked[SO]) addCallArgs(methodName string, args ...interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.callArgsByMethodName[methodName] == nil {
		s.callArgsByMethodName[methodName] = make([][]interface{}, 0)
	}
	s.callArgsByMethodName[methodName] = append(s.callArgsByMethodName[methodName], args)
}

func ValidateStubSignature(stubbedObject interface{}, methodName string, fn interface{}) {
	fnVal := reflect.ValueOf(fn)
	if fnVal.Kind() != reflect.Func {
		panic("fn must be a function")
	}
	soVal := reflect.ValueOf(stubbedObject)
	soType := soVal.Type()

	soName := soType.Name()
	if soType.Kind() == reflect.Ptr {
		soName = soType.Elem().Name()
	}

	soValMethod, foundMethod := soType.MethodByName(methodName)
	if !foundMethod {
		fmt.Println("method count: ", soType.NumMethod())
		panic(fmt.Sprintf("methodName (%s) must be a method of %s", methodName, soName))
	}

	// assert i/o count matches
	fnType := fnVal.Type()
	soFuncType := soValMethod.Func.Type()
	fnNumIn := fnType.NumIn()
	soFuncNumIn := soFuncType.NumIn()

	if soFuncNumIn != fnNumIn {
		//	NOTE: this compares fn to the "under the hood" GENERATED function based upon the method signature
		//	this adds the receiver as the first argument
		panic(fmt.Sprintf("fn must have the same arg count as %s.%s's func signature\nDid you forget to include the receiver arg?", soName, methodName))
	}
	fnNumOut := fnType.NumOut()
	soFuncNumOut := soFuncType.NumOut()
	if soFuncNumOut != fnNumOut {
		panic(fmt.Sprintf("fn must have the same return count as %s.%s's func signature", soName, methodName))
	}

	// assert each i/o type match
	for i := 0; i < fnNumIn; i++ {
		if soFuncType.In(i) != fnType.In(i) {
			panic(fmt.Sprintf("fn param #%d must have the same type as %s.%s's func signature:"+
				"\n\t%s (expected) is not %s (actual)", i, soName, methodName, soFuncType.In(i), fnType.In(i)))
		}
	}
	for i := 0; i < fnNumOut; i++ {
		if soFuncType.Out(i) != fnType.Out(i) {
			panic(fmt.Sprintf("fn return #%d must have the same type as %s.%s's func signature:"+
				"\n\t%s (expected) is not %s (actual)", i, soName, methodName, soFuncType.Out(i), fnType.In(i)))
		}
	}
}
