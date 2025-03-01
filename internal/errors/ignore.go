/*
Copyright 2025 containeroo

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file is copied from the source at
https://github.com/fluxcd/helm-controller/blob/25c6bb691df30a3c2fd8a4b19ce65bc134158c90/internal/errors/ignore.go
*/

package errors

// Ignore returns nil if err is equal to any of the errs.
func Ignore(err error, errs ...error) error {
	if IsOneOf(err, errs...) {
		return nil
	}
	return err
}
