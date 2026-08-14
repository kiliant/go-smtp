#!/bin/sh
set -eu

sed "s/__GOSMTP_PORT__/${GOSMTP_PORT}/g" \
	/etc/exim4/exim4.conf.template > /etc/exim4/exim4.conf

exec /usr/sbin/exim4 -bd -q30m -v
