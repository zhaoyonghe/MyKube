/*
Copyright 2015 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	. "k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	utiltesting "k8s.io/client-go/util/testing"
)

func parseSelectorOrDie(s string) fields.Selector {
	selector, err := fields.ParseSelector(s)
	if err != nil {
		panic(err)
	}
	return selector
}

// buildQueryValues is a convenience function for knowing if a namespace should be in a query param or not
func buildQueryValues(query url.Values) url.Values {
	v := url.Values{}
	for key, values := range query {
		for _, value := range values {
			v.Add(key, value)
		}
	}

	return v
}

func buildLocation(resourcePath string, query url.Values) string {
	return resourcePath + "?" + query.Encode()
}

func TestListWatchesCanList(t *testing.T) {
	fieldSelectorQueryParamName := metav1.FieldSelectorQueryParam("v1")
	table := []struct {
		location      string
		resource      string
		namespace     string
		fieldSelector fields.Selector
	}{
		// Node
		{
			location:      "/api/v1/nodes",
			resource:      "nodes",
			namespace:     metav1.NamespaceAll,
			fieldSelector: parseSelectorOrDie(""),
		},
		// pod with "assigned" field selector.
		{
			location: buildLocation(
				"/api/v1/pods",
				buildQueryValues(url.Values{fieldSelectorQueryParamName: []string{"spec.host="}})),
			resource:      "pods",
			namespace:     metav1.NamespaceAll,
			fieldSelector: fields.Set{"spec.host": ""}.AsSelector(),
		},
		// pod in namespace "foo"
		{
			location: buildLocation(
				"/api/v1/namespaces/foo/pods",
				buildQueryValues(url.Values{fieldSelectorQueryParamName: []string{"spec.host="}})),
			resource:      "pods",
			namespace:     "foo",
			fieldSelector: fields.Set{"spec.host": ""}.AsSelector(),
		},
	}
	for _, item := range table {
		handler := utiltesting.FakeHandler{
			StatusCode:   500,
			ResponseBody: "",
			T:            t,
		}
		server := httptest.NewServer(&handler)
		defer server.Close()
		client := clientset.NewForConfigOrDie(&restclient.Config{Host: server.URL, ContentConfig: restclient.ContentConfig{GroupVersion: &schema.GroupVersion{Group: "", Version: "v1"}}})
		lw := NewListWatchFromClient(client.CoreV1().RESTClient(), item.resource, item.namespace, item.fieldSelector)
		lw.DisableChunking = true
		// This test merely tests that the correct request is made.
		lw.List(metav1.ListOptions{})
		handler.ValidateRequest(t, item.location, "GET", nil)
	}
}

func TestListWatchesCanWatch(t *testing.T) {
	fieldSelectorQueryParamName := metav1.FieldSelectorQueryParam("v1")
	table := []struct {
		rv            string
		location      string
		resource      string
		namespace     string
		fieldSelector fields.Selector
	}{
		// Node
		{
			location: buildLocation(
				"/api/v1/nodes",
				buildQueryValues(url.Values{"watch": []string{"true"}})),
			rv:            "",
			resource:      "nodes",
			namespace:     metav1.NamespaceAll,
			fieldSelector: parseSelectorOrDie(""),
		},
		{
			location: buildLocation(
				"/api/v1/nodes",
				buildQueryValues(url.Values{"resourceVersion": []string{"42"}, "watch": []string{"true"}})),
			rv:            "42",
			resource:      "nodes",
			namespace:     metav1.NamespaceAll,
			fieldSelector: parseSelectorOrDie(""),
		},
		// pod with "assigned" field selector.
		{
			location: buildLocation(
				"/api/v1/pods",
				buildQueryValues(url.Values{fieldSelectorQueryParamName: []string{"spec.host="}, "resourceVersion": []string{"0"}, "watch": []string{"true"}})),
			rv:            "0",
			resource:      "pods",
			namespace:     metav1.NamespaceAll,
			fieldSelector: fields.Set{"spec.host": ""}.AsSelector(),
		},
		// pod with namespace foo and assigned field selector
		{
			location: buildLocation(
				"/api/v1/namespaces/foo/pods",
				buildQueryValues(url.Values{fieldSelectorQueryParamName: []string{"spec.host="}, "resourceVersion": []string{"0"}, "watch": []string{"true"}})),
			rv:            "0",
			resource:      "pods",
			namespace:     "foo",
			fieldSelector: fields.Set{"spec.host": ""}.AsSelector(),
		},
	}

	for _, item := range table {
		handler := utiltesting.FakeHandler{
			StatusCode:   500,
			ResponseBody: "",
			T:            t,
		}
		server := httptest.NewServer(&handler)
		defer server.Close()
		client := clientset.NewForConfigOrDie(&restclient.Config{Host: server.URL, ContentConfig: restclient.ContentConfig{GroupVersion: &schema.GroupVersion{Group: "", Version: "v1"}}})
		lw := NewListWatchFromClient(client.CoreV1().RESTClient(), item.resource, item.namespace, item.fieldSelector)
		// This test merely tests that the correct request is made.
		lw.Watch(metav1.ListOptions{ResourceVersion: item.rv})
		handler.ValidateRequest(t, item.location, "GET", nil)
	}
}

type lw struct {
	list  runtime.Object
	watch watch.Interface
}

func (w lw) List(options metav1.ListOptions) (runtime.Object, error) {
	return w.list, nil
}

func (w lw) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return w.watch, nil
}

func TestListWatchUntil(t *testing.T) {
	fw := watch.NewFake()
	go func() {
		obj := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				ResourceVersion: "2",
			},
		}
		fw.Modify(obj)
	}()
	listwatch := lw{
		list: &v1.PodList{
			ListMeta: metav1.ListMeta{
				ResourceVersion: "1",
			},
			Items: []v1.Pod{{}},
		},
		watch: fw,
	}

	conditions := []watchtools.ConditionFunc{
		func(event watch.Event) (bool, error) {
			t.Logf("got %#v", event)
			return event.Type == watch.Added, nil
		},
		func(event watch.Event) (bool, error) {
			t.Logf("got %#v", event)
			return event.Type == watch.Modified, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lastEvent, err := watchtools.ListWatchUntil(ctx, listwatch, conditions...)
	if err != nil {
		t.Fatalf("expected nil error, got %#v", err)
	}
	if lastEvent == nil {
		t.Fatal("expected an event")
	}
	if lastEvent.Type != watch.Modified {
		t.Fatalf("expected MODIFIED event type, got %v", lastEvent.Type)
	}
	if got, isPod := lastEvent.Object.(*v1.Pod); !isPod {
		t.Fatalf("expected a pod event, got %#v", got)
	}
}
