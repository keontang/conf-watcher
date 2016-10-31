# conf-watcher

conf-watcher is used to watch the config/env files of softwares, once one config/env file changes, conf-watcher can reload the corresponding software.

## Terminology

### WatchFile

The file we want to watch is called a watchFile.

For example, we want to watch `/var/run/flannel/subnet.env`, then `/var/run/flannel/subnet.env` is a watchFile.

### Watchlist

The file contains watchFiles and watchFiles' corresponding commands is called a watchlist. Watchlist file is a json file, the format of the watchlist file is just as follows:

```
[
    {"watchFile": "/var/run/flannel/subnet.env", "commmad": "service docker restart"},
    ...
]
```

Once a watchFile is changed, then the corresponding command will be executed.

### Watchlist Directory

The watchlist directory contains watchlist files. When a watchlist file is added into (removed from) the  watchlist direcotry, the watchFiles in the watchlist file will be watching (unwatching).

Default watchlist directory is `/var/lib/conf-watcher`, we can use the option `--watchlist-dir` to change it.

## Running

If we use default watchlist directory `/var/lib/conf-watcher`, then we can run:

```
root@ubuntu:~/work/src/github.com/caicloud/conf-watcher# ./conf-watcher
```

We can also specify another watchlist directory, for example:

```
root@ubuntu:~/work/src/github.com/caicloud/conf-watcher# ./conf-watcher --watchlist-dir=$HOME/conf-watcher
```

## Install

```
# ./tools/install.sh
```
