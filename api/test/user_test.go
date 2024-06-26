package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jatolentino/tutorialspoint/core/user"
)

type userTest struct {
	*TestEnv
}

func TestUser(t *testing.T) {
	env, err := NewTestEnv(t, "user_test")
	if err != nil {
		t.Fatalf("initializing test env: %v", err)
	}

	ut := &userTest{env}

	usr := ut.getUserOK(t)
	ut.adminGetUserOK(t, usr.ID)
	ut.getUserAfterLogout(t, usr.ID)
	ut.getUserUnauth(t, usr.ID)

	ut.createAdminOK(t)
	ut.createUserOK(t)
	ut.createUserUnauth(t)
	ut.createUserExistent(t)
}

func (ut *userTest) getUserOK(t *testing.T) user.User {
	usr, err := Signup(ut.Server, user.UserSignup{
		Name:            "Paolo Calao",
		Email:           "jatolentino@test.com",
		Password:        "pass12345678",
		PasswordConfirm: "pass12345678",
	})

	if err != nil {
		t.Fatal(err)
	}

	if err := Activate(ut.Server, usr.Email, ut.Mailer); err != nil {
		t.Fatal(err)
	}

	if err := Login(ut.Server, "jatolentino@test.com", "pass12345678"); err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodGet, ut.Server.URL+"/users/"+usr.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}

	var got user.User
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("cannot unmarshal created user: %v", err)
	}

	exp := got
	exp.Name = "Paolo Calao"
	exp.Email = "jatolentino@test.com"
	exp.Role = "USER"

	if diff := cmp.Diff(got, exp); diff != "" {
		t.Fatalf("wrong user payload. Diff: \n%s", diff)
	}

	return got
}

func (ut *userTest) adminGetUserOK(t *testing.T, id string) {
	if err := Login(ut.Server, ut.AdminEmail, ut.AdminPass); err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodGet, ut.Server.URL+"/users/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}

	var got user.User
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("cannot unmarshal created user: %v", err)
	}

	exp := got
	exp.Name = "Paolo Calao"
	exp.Email = "jatolentino@test.com"
	exp.Role = "USER"

	if diff := cmp.Diff(got, exp); diff != "" {
		t.Fatalf("wrong user payload. Diff: \n%s", diff)
	}
}

// getUserAfterLogout also checks that auth logout is working.
// That would be more difficult to be checked in `auth_test.go`.
func (ut *userTest) getUserAfterLogout(t *testing.T, id string) {

	// Login as admin, which should be able to fetch any user.
	if err := Login(ut.Server, ut.AdminEmail, ut.AdminPass); err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodGet, ut.Server.URL+"/users/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}

	if w.StatusCode != 200 {
		t.Fatalf("admin should be able to fetch user details")
	}

	// Now logout and fetch the user again, this time an error should be returned.
	if err := Logout(ut.Server); err != nil {
		t.Fatal(err)
	}

	r, err = http.NewRequest(http.MethodGet, ut.Server.URL+"/users/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}

	w, err = ut.Server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}

	if w.StatusCode == 200 {
		t.Fatalf("logged out admin should not be able to fetch user details")
	}

}

func (ut *userTest) getUserUnauth(t *testing.T, id string) {
	if err := Login(ut.Server, ut.UserEmail, ut.UserPass); err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodGet, ut.Server.URL+"/users/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}

	if w.StatusCode != 401 {
		t.Fatalf("a user should not be able to fetch other users")
	}
}

func (ut *userTest) createAdminOK(t *testing.T) {
	if err := Login(ut.Server, ut.AdminEmail, ut.AdminPass); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	usr := user.UserNew{
		Name:            "First User",
		Email:           "first@test.com",
		Role:            "ADMIN",
		Password:        "testpass",
		PasswordConfirm: "testpass",
	}

	body, err := json.Marshal(&usr)
	if err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodPost, ut.URL+"/users", bytes.NewBuffer(body))
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Body.Close()

	if w.StatusCode != http.StatusCreated {
		t.Fatalf("can't create user: status code %s", w.Status)
	}

	var got user.User
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("cannot unmarshal created user: %v", err)
	}

	exp := got
	exp.Name = "First User"
	exp.Email = "first@test.com"
	exp.Role = "ADMIN"

	if diff := cmp.Diff(got, exp); diff != "" {
		t.Fatalf("wrong user payload. Diff: \n%s", diff)
	}
}

func (ut *userTest) createUserUnauth(t *testing.T) {
	if err := Login(ut.Server, ut.UserEmail, ut.UserPass); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	usr := user.UserNew{
		Name:            "Second User",
		Email:           "second@test.com",
		Role:            "USER",
		Password:        "testpass",
		PasswordConfirm: "testpass",
	}

	body, err := json.Marshal(&usr)
	if err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodPost, ut.URL+"/users", bytes.NewBuffer(body))
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Body.Close()

	if w.StatusCode != http.StatusUnauthorized {
		t.Fatal("users cannot create other users")
	}
}

func (ut *userTest) createUserExistent(t *testing.T) {
	if err := Login(ut.Server, ut.AdminEmail, ut.AdminPass); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	usr := user.UserNew{
		Name:            "First User",
		Email:           "first@test.com",
		Role:            "ADMIN",
		Password:        "testpass",
		PasswordConfirm: "testpass",
	}

	body, err := json.Marshal(&usr)
	if err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodPost, ut.URL+"/users", bytes.NewBuffer(body))
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Body.Close()

	if w.StatusCode != http.StatusConflict {
		t.Fatal("cannot create already existing user")
	}
}

func (ut *userTest) createUserOK(t *testing.T) {
	if err := Login(ut.Server, ut.AdminEmail, ut.AdminPass); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	usr := user.UserNew{
		Name:            "Third User",
		Email:           "third@test.com",
		Role:            "USER",
		Password:        "testpass",
		PasswordConfirm: "testpass",
	}

	body, err := json.Marshal(&usr)
	if err != nil {
		t.Fatal(err)
	}

	r, err := http.NewRequest(http.MethodPost, ut.URL+"/users", bytes.NewBuffer(body))
	if err != nil {
		t.Fatal(err)
	}

	w, err := ut.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Body.Close()

	if w.StatusCode != http.StatusCreated {
		t.Fatalf("can't create user: status code %s", w.Status)
	}

	var got user.User
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("cannot unmarshal created user: %v", err)
	}

	exp := got
	exp.Name = "Third User"
	exp.Email = "third@test.com"
	exp.Role = "USER"

	if diff := cmp.Diff(got, exp); diff != "" {
		t.Fatalf("wrong user payload. Diff: \n%s", diff)
	}
}
