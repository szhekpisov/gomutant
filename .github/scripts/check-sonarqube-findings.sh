#!/usr/bin/env bash

set -euo pipefail

: "${SONAR_PROJECT_KEY:?SONAR_PROJECT_KEY is required}"
: "${PULL_REQUEST_NUMBER:?PULL_REQUEST_NUMBER is required}"
: "${HEAD_SHA:?HEAD_SHA is required}"

SONAR_API_URL="${SONAR_API_URL:-https://sonarcloud.io/api}"
SONAR_MAX_ATTEMPTS="${SONAR_MAX_ATTEMPTS:-60}"
SONAR_POLL_SECONDS="${SONAR_POLL_SECONDS:-10}"

if [[ ! "${PULL_REQUEST_NUMBER}" =~ ^[0-9]+$ ]]; then
  echo "PULL_REQUEST_NUMBER must be numeric" >&2
  exit 2
fi
if [[ ! "${HEAD_SHA}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "HEAD_SHA must be a full lowercase commit SHA" >&2
  exit 2
fi
if [[ ! "${SONAR_MAX_ATTEMPTS}" =~ ^[1-9][0-9]*$ ]]; then
  echo "SONAR_MAX_ATTEMPTS must be a positive integer" >&2
  exit 2
fi
if [[ ! "${SONAR_POLL_SECONDS}" =~ ^[0-9]+$ ]]; then
  echo "SONAR_POLL_SECONDS must be a non-negative integer" >&2
  exit 2
fi

analysis_sha=""
for ((attempt = 1; attempt <= SONAR_MAX_ATTEMPTS; attempt++)); do
  pull_requests="$(
    curl \
      --fail \
      --retry 3 \
      --retry-all-errors \
      --silent \
      --show-error \
      --get \
      --data-urlencode "project=${SONAR_PROJECT_KEY}" \
      "${SONAR_API_URL}/project_pull_requests/list"
  )"
  analysis_sha="$(
    jq --arg pr "${PULL_REQUEST_NUMBER}" -r \
      'first(.pullRequests[] | select(.key == $pr) | .commit.sha) // empty' \
      <<<"${pull_requests}"
  )"

  if [[ "${analysis_sha}" == "${HEAD_SHA}" ]]; then
    break
  fi

  echo "Waiting for SonarQube analysis of ${HEAD_SHA} (attempt ${attempt}/${SONAR_MAX_ATTEMPTS})"
  if ((attempt < SONAR_MAX_ATTEMPTS)); then
    sleep "${SONAR_POLL_SECONDS}"
  fi
done

if [[ "${analysis_sha}" != "${HEAD_SHA}" ]]; then
  echo "::error title=SonarQube analysis missing::SonarQube did not analyze the current PR head ${HEAD_SHA}" >&2
  exit 1
fi

issues="$(
  curl \
    --fail \
    --retry 3 \
    --retry-all-errors \
    --silent \
    --show-error \
    --get \
    --data-urlencode "componentKeys=${SONAR_PROJECT_KEY}" \
    --data-urlencode "pullRequest=${PULL_REQUEST_NUMBER}" \
    --data-urlencode "issueStatuses=OPEN,CONFIRMED" \
    --data-urlencode "sinceLeakPeriod=true" \
    --data-urlencode "ps=1" \
    "${SONAR_API_URL}/issues/search"
)"
finding_count="$(jq -er '.total | numbers' <<<"${issues}")"
details_url="https://sonarcloud.io/project/issues?id=${SONAR_PROJECT_KEY}&pullRequest=${PULL_REQUEST_NUMBER}&issueStatuses=OPEN%2CCONFIRMED&sinceLeakPeriod=true"

if ((finding_count > 0)); then
  echo "::error title=SonarQube findings::SonarQube reported ${finding_count} new open finding(s): ${details_url}" >&2
  exit 1
fi

echo "SonarQube reported zero new open findings for PR #${PULL_REQUEST_NUMBER} at ${HEAD_SHA}."
