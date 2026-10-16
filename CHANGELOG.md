# Changelog

## [0.3.0](https://github.com/ckw1206/tollgate/compare/v0.2.0...v0.3.0) (2026-10-16)


### Features

* **budget:** real budget system — window correctness fixes + 80%/100% webhook ([#36](https://github.com/ckw1206/tollgate/issues/36)) ([927fb4a](https://github.com/ckw1206/tollgate/commit/927fb4aa268b67b1d9c4839c9735f0895461cefe))
* **console:** create users directly, consolidated edit, model access dropdowns ([b99da24](https://github.com/ckw1206/tollgate/commit/b99da24dbbe2d9252b916b19b3003d23f337407b))
* **console:** model access tab — per-group MultiSelect rows replacing the checkbox grid ([97d2406](https://github.com/ckw1206/tollgate/commit/97d2406e8057ce2d11357d5279c66dbd0442d720))
* **console:** POST /users account creation + password reset in UserUpdate ([9c4f83f](https://github.com/ckw1206/tollgate/commit/9c4f83ff2e565b1fe9a2a69fccd671eafeeea922))
* **console:** user management UI — create dialog, consolidated edit dialog, simplified users table ([5e431bd](https://github.com/ckw1206/tollgate/commit/5e431bd897bfad819a3137f4cbca4093a417604a))
* optional model context_window, exposed in /v1/models ([ebb5199](https://github.com/ckw1206/tollgate/commit/ebb51999096ee37e4c6108f3a9d905e04930e5d1))
* **usage:** admin global dashboard — per-user/group boards, per-user Top models, token units, unified time range ([#37](https://github.com/ckw1206/tollgate/issues/37)) ([7da10d8](https://github.com/ckw1206/tollgate/commit/7da10d8d05a6d9f9891b2dc9f27cd4be82442cb1))
* **web:** ModelPicker — portaled allowed-models cell on the access tab; revert Table scrollX ([58e670a](https://github.com/ckw1206/tollgate/commit/58e670a5b3edf4b23d8f289d4995c2aa2dc1116f))
* **web:** pure model picker logic — state, diff, footer text and provider groups ([af9f953](https://github.com/ckw1206/tollgate/commit/af9f953923078eea81cfb00afea97c34405beeee))


### Fixes

* **providers:** retry upstream POST on a stale keep-alive connection ([6d37f74](https://github.com/ckw1206/tollgate/commit/6d37f74884f54fb9880352d89aa11b849436a6db))
* **providers:** retry upstream POST on a stale keep-alive connection ([#35](https://github.com/ckw1206/tollgate/issues/35)) ([089d968](https://github.com/ckw1206/tollgate/commit/089d968bf1ba0384c3fb695e8abdeedfbd023a74))
* **web:** guard Escape while disable-confirm is open; fix stale comment ([55b0c3d](https://github.com/ckw1206/tollgate/commit/55b0c3d8770da1cb198e32d7a1d90d2446ae02b6))
* **web:** let the access table stop clipping the MultiSelect popover ([ce4f856](https://github.com/ckw1206/tollgate/commit/ce4f856888b5e257d972d05331ebf96bb6abb7d9))
* **web:** measure ModelPicker popover height before flipping up ([7851ae7](https://github.com/ckw1206/tollgate/commit/7851ae7b514aeca0eb120da4578045b179fece28))
* **web:** truncate allowed-models column, reword users models cell, clear hidden extras ([35f67e4](https://github.com/ckw1206/tollgate/commit/35f67e43557acc038c2d4d0a2d601feab7f7887f))

## [0.2.0](https://github.com/ckw1206/tollgate/compare/v0.1.0...v0.2.0) (2026-10-14)


### ⚠ BREAKING CHANGES

* **api:** a model's credential endpoint now wins over its own api_base, and models without a credential require an api_base; migration 007 copies unambiguous endpoints onto credentials (check before upgrading: docs/operations.md)

### Features

* **api:** a model's credential endpoint now wins over its own api_base, and models without a credential require an api_base; migration 007 copies unambiguous endpoints onto credentials (check before upgrading: docs/operations.md) ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* **api:** GET /console/api/v1/spend/requests and GET /spend?group_by=model,day ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* **console:** audit log Rows per page (25/50/100/200); GET /audit caps limit at 200 ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* **console:** clean landing page at /welcome, with Sign in and first-run Setup moved to their own page; signed-out / goes to /welcome ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* **console:** dashboard range (Today, 7/30/90 days, custom) kept in the URL and applied to every panel ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* **console:** Model access grid that saves on click with Undo, Allowed models on groups, Extra models per user, and a "Your access" column on Models for members and viewers ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* **console:** six-item nav with page tabs (Models · Credentials; Users · Groups · Invites · Model access); old /users, /groups, /invites and /credentials redirect ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* **console:** Top models Bars | Trend view, and Recent requests with Owner, API key and Show 10/25/50/100 ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))
* model access per group plus per-user extra grants; the gateway refuses other models with 403 model_not_allowed and /v1/models lists only allowed ones (migration 008) ([ead27f8](https://github.com/ckw1206/tollgate/commit/ead27f8c623e7361bc05bc719a6f4314d514fe2a))


### Dependencies

* **deps:** bump Docker base images and GitHub Actions ([#18](https://github.com/ckw1206/tollgate/issues/18)) ([6d071cd](https://github.com/ckw1206/tollgate/commit/6d071cd9c48890951e8daa12a3e7e46a430ab82f))
