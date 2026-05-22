# Auth

Endpoints under the `Auth` tag. Auto-generated from [`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.

> Run `make api-docs` after changing handler annotations to regenerate this file.

## Operations

| Method | Path | Summary |
|--------|------|---------|
| `GET` | [`/api/auth/check`](#op-get-api-auth-check) | Check session validity |
| `POST` | [`/api/auth/login`](#op-post-api-auth-login) | Log in with email + password |
| `POST` | [`/api/auth/logout`](#op-post-api-auth-logout) | Log out (clear session cookie) |
| `GET` | [`/api/auth/me`](#op-get-api-auth-me) | Get current user profile |
| `POST` | [`/api/auth/register`](#op-post-api-auth-register) | Register a new user |

### `GET /api/auth/check` {#op-get-api-auth-check}
Check session validity

**Auth:** `CookieAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AuthStatusResponse`](#schema-authstatusresponse) |
| `401` | unauthorized | `string` |


### `POST /api/auth/login` {#op-post-api-auth-login}
Log in with email + password
Validates credentials; on success sets the session cookie and returns {"status":"ok"}.

**Request body**

Content-Type: `application/json`

Schema: [`AuthCredentials`](#schema-authcredentials)

```yaml
{
  email: string
  password: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AuthStatusResponse`](#schema-authstatusresponse) |
| `400` | bad request | `string` |
| `401` | invalid credentials | `string` |


### `POST /api/auth/logout` {#op-post-api-auth-logout}
Log out (clear session cookie)

**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AuthStatusResponse`](#schema-authstatusresponse) |


### `GET /api/auth/me` {#op-get-api-auth-me}
Get current user profile

**Auth:** `CookieAuth`


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `200` | OK | [`AuthMeResponse`](#schema-authmeresponse) |
| `404` | user not found | `string` |


### `POST /api/auth/register` {#op-post-api-auth-register}
Register a new user
On success returns the new user id. The first registered user becomes admin.

**Request body**

Content-Type: `application/json`

Schema: [`AuthCredentials`](#schema-authcredentials)

```yaml
{
  email: string
  password: string
}
```


**Responses**

| Status | Description | Schema |
|--------|-------------|--------|
| `201` | Created | [`AuthRegisterResponse`](#schema-authregisterresponse) |
| `400` | bad request / email and password required | `string` |
| `409` | email already taken | `string` |
| `500` | internal error / failed to create user | `string` |


## Schemas

### `AuthCredentials` {#schema-authcredentials}

```yaml
{
  email: string
  password: string
}
```

### `AuthMeResponse` {#schema-authmeresponse}

```yaml
{
  email: string
  id: string
  name?: string
  picture?: string
  role: string
}
```

### `AuthRegisterResponse` {#schema-authregisterresponse}

```yaml
{
  email: string
  id: string
}
```

### `AuthStatusResponse` {#schema-authstatusresponse}

```yaml
{
  status: string
}
```
