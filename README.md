This is the code for the [bitcoin++ website](https://btcpp.dev). (Conference [X Account](https://x.com/btcplusplus))

All of the configuration values live in a `config.toml` file, which is missing from this repo on purpose.


## Setup Dependencies

We use nix for this. Installs go + tailwindcss + air dependencies for Makefile.

```
	nix develop
```


## To run for development

```
	make dev-run
```


## To build

```
  make build
```


This will put all the files necessary to serve the site into `target/`


## Recording autopublisher

The recordings dashboard can auto-publish Notion Recording rows that have `FileURI` and `PublishAt` set. Enable the background worker with:

```
RECORDINGS_AUTOPUBLISH_ENABLED=true
RECORDINGS_AUTOPUBLISH_POLL_SEC=60
RECORDINGS_NOTIFY_EMAIL=nifty@btcpp.dev
SOCIAL_STATE_KEY=<base64-encoded 32-byte key>
```

YouTube OAuth tokens and the X Chrome profile are encrypted into Spaces because DigitalOcean App Platform does not persist local disk across deploys. The default object keys are:

```
YOUTUBE_TOKEN_OBJECT=private/social/youtube-token.json.enc
X_PROFILE_ARCHIVE_OBJECT=private/social/x-chrome-profile.tgz.enc
```

Set `X_UPLOADER_ENABLED=true` on exactly one running app component. To repair X auth, run the app locally with the same Spaces credentials plus `X_BROWSER_HEADED=true`, use the recordings admin page's Bootstrap X action, finish the x.com login in Chrome, then run Test X auth.

Note that the Github actions deployer uses Docker and isn't nix-aware, so for now you *must* make and check-in any CSS changes before deploying.

CSS updates are made automatically by `dev-run`, so this shouldn't be too hard.


## Deploy Testing

Currently, we deploy the app using Digital Ocean, using the `Dockerfile`. Sometimes it's useful to test building changes locally. For this, I'd recommend using the `doctl` app.

Instructions [here](https://docs.digitalocean.com/products/app-platform/how-to/build-locally/), but in brief.

```
doctl app dev build
```

Then follow the instructions to run.

FIXME: currently it picks up the config.toml file; remove this and use .env.


Let's add an example fixme!
