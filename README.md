# conf-watcher

conf-watcher is used to act upon config/env file changes.

## Terminology

### Watchfile

The file to be watched is called a Watchfile.

For example, `/var/run/flannel/subnet.env` is a Watchfile if conf-watcher is configured to watch it.

### Watchlist

Watchlist is a list of item which contains a Watchfile and its corresponding commands. Once Watchfile changes,
the corresponding command will be executed. Watchlist is specified in json format.

For example, following Watchlist contains a single item. If configured so, conf-watcher will reload docker
when `/var/run/flannel/subnet.env` file changes.

```
[
  { "watchFile": "/var/run/flannel/subnet.env", "commmad": "service docker restart" }
]
```

### Watchlist Directory

The Watchlist Directory contains Watchlist files. When a WatchList file is added into (removed from) the Watchlist
Direcotry, the Watchfiles in the Watchlist file will be watching (unwatching).

Default Watchlist Directory is `/var/lib/conf-watcher`; we can use the option `--watchlist-dir` to change it.

## Running

To run conf-watcher with default watchlist directory, run:

```
root@ubuntu:~/work/src/github.com/caicloud/conf-watcher# ./conf-watcher
```

Use `--watchlist-dir` to specify another watchlist directory, e.g.:

```
root@ubuntu:~/work/src/github.com/caicloud/conf-watcher# ./conf-watcher --watchlist-dir=$HOME/conf-watcher
```

## Install

```
# ./tools/install.sh
```
