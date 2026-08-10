#!/bin/bash

set -euo pipefail

# $1 = docs build output directory

site_dir="$1"
public_dir="$BUILD_WORKSPACE_DIRECTORY/public"

if [ -d "$public_dir" ]; then
	chmod -R u+w "$public_dir"
fi

mkdir -p "$public_dir"
cp -r "$site_dir"/* "$public_dir/"
echo "Copied site to $public_dir"
ls -la "$public_dir"
