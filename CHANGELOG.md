# Changelog

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
