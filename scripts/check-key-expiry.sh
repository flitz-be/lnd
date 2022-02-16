#!/bin/bash

DIR="$(cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd)"
KEYS=$(find $DIR/keys -name "*.asc")
TWO_WEEKS=$(date -d "2 weeks" +%s)

for key in "$KEYS"; do
   cat $key | gpg --with-colons --import-options show-only --import | awk -F':' -vtwoweeks=$TWO_WEEKS '{ if (($1 == "pub" || $1 == "sub") && $7 < twoweeks) {print $1 " " $5 "key expires in less than two weeks"} }'
done
