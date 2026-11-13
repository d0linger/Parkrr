'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

test('CI and publishing download and verify the same pinned Trivy archive', () => {
  const workflows = path.join(__dirname, '../../.github/workflows');
  const ci = fs.readFileSync(path.join(workflows, 'ci.yml'), 'utf8');
  const publish = fs.readFileSync(path.join(workflows, 'docker-publish.yml'), 'utf8');
  const version = ci.match(/TRIVY_VERSION=(\d+\.\d+\.\d+)/)?.[1];
  const checksum = ci.match(/TRIVY_SHA256=([a-f0-9]{64})/)?.[1];
  assert.ok(version, 'CI must pin a Trivy version');
  assert.ok(checksum, 'CI must pin the archive checksum');
  const url = `https://github.com/aquasecurity/trivy/releases/download/v${version}/trivy_${version}_Linux-64bit.tar.gz`;
  assert.ok(publish.includes(url), 'publishing must download the CI-pinned Trivy archive');
  assert.ok(publish.includes(`'${checksum}'`), 'publishing must verify the same archive checksum');
});
