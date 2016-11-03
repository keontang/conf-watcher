#! /bin/bash

# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0

# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ROOT_DIR=$(cd $(dirname ${BASH_SOURCE:-$0}); pwd)/..
SERVICE_NAME="conf-watcher"
DIST_PATH="${ROOT_DIR}/dist"
TARGET="conf-watcher"

if ! [ -x ${DIST_PATH}/${TARGET} ]; then
    cd ${ROOT_DIR}
    make || exit 1
    cd -
fi

os_distro=$(grep '^NAME=' /etc/os-release | sed s'/NAME=//' | sed s'/"//g' | awk '{print $1}' | tr '[:upper:]' '[:lower:]')

if [ "${os_distro}" == "ubuntu" ]; then
    set -x
    service ${SERVICE_NAME} stop 1>/dev/null 2>&1
    cp -f ${DIST_PATH}/${TARGET} /usr/bin/
    cp ${ROOT_DIR}/tools/ubuntu/${SERVICE_NAME}.conf /etc/init/
    service ${SERVICE_NAME} start
elif [ "${os_distro}" == "centos" ]; then
    set -x
    systemctl stop ${SERVICE_NAME} 1>/dev/null 2>&1
    cp -f ${DIST_PATH}/${TARGET} /usr/bin/
    cp ${ROOT_DIR}/tools/centos/${SERVICE_NAME}.service /etc/systemd/system/
    systemctl enable ${SERVICE_NAME}
    systemctl --system daemon-reload
    systemctl start ${SERVICE_NAME}
else
    echo "Can't support os distro '${os_distro}' currently" >&2
    exit 1
fi
