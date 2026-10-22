## What's Changed
* Bump @typescript-eslint/eslint-plugin from 8.67.0 to 8.68.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4661
* chore: update Node versions by @github-actions[bot] in https://github.com/actions/runner/pull/4665
* Bump browserslist from 4.28.5 to 4.28.9 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4677
* Update Docker to v29.8.0 and Buildx to v0.37.0 by @github-actions[bot] in https://github.com/actions/runner/pull/4676
* Update Node.js 20 removal date to September 23rd, 2026 by @salmanmkc in https://github.com/actions/runner/pull/4685
* Bump @typescript-eslint/parser from 8.68.0 to 8.69.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4673
* Bump Microsoft.DevTunnels.Connections from 1.3.51 to 1.3.56 by @dependabot[bot] in https://github.com/actions/runner/pull/4667
* Bump github/codeql-action from 4.37.8 to 4.37.9 by @dependabot[bot] in https://github.com/actions/runner/pull/4664
* Bump lint-staged from 17.3.0 to 17.5.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4663
* Bump System.Formats.Asn1 and System.Security.Cryptography.Pkcs by @dependabot[bot] in https://github.com/actions/runner/pull/4688
* Bump @typescript-eslint/parser from 8.69.0 to 8.70.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4705
* Bump js-yaml from 4.3.1 to 4.3.2 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4696
* Bump lint-staged from 17.5.0 to 17.5.1 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4715
* chore: update Node versions by @github-actions[bot] in https://github.com/actions/runner/pull/4716
* Bump github/codeql-action from 4.37.9 to 4.38.0 by @dependabot[bot] in https://github.com/actions/runner/pull/4713
* Bump @typescript-eslint/eslint-plugin from 8.68.0 to 8.70.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4714
* Update Buildx to v0.37.1 by @github-actions[bot] in https://github.com/actions/runner/pull/4712
* Bump System.Text.Encoding.CodePages from 10.0.11 to 10.0.12 by @dependabot[bot] in https://github.com/actions/runner/pull/4701
* Update dotnet sdk to latest version @8.0.425 by @github-actions[bot] in https://github.com/actions/runner/pull/4717
* Bump github/codeql-action from 4.38.0 to 4.38.1 by @dependabot[bot] in https://github.com/actions/runner/pull/4731
* Update Docker to v29.8.1 by @github-actions[bot] in https://github.com/actions/runner/pull/4730
* Accept $/ self-repository references in action manifests by @nodeselector in https://github.com/actions/runner/pull/4669
* Bump Microsoft.DevTunnels.Connections from 1.3.56 to 1.3.60 by @dependabot[bot] in https://github.com/actions/runner/pull/4733
* Bump Moq from 4.20.72 to 4.21.0 by @dependabot[bot] in https://github.com/actions/runner/pull/4738
* Bump @typescript-eslint/eslint-plugin from 8.70.0 to 8.70.1 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4740
* Bump @typescript-eslint/parser from 8.70.0 to 8.70.1 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4739
* Bump github/codeql-action from 4.38.1 to 4.38.2 by @dependabot[bot] in https://github.com/actions/runner/pull/4741
* Bump undici from 6.28.0 to 6.29.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4744
* Bump lint-staged from 17.5.1 to 17.6.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4746
* Include owned session ID when deleting broker sessions by @TingluoHuang in https://github.com/actions/runner/pull/4743
* Bump brace-expansion in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4747
* Bump Azure.Storage.Blobs from 12.29.2 to 12.30.0 by @dependabot[bot] in https://github.com/actions/runner/pull/4749
* Update Docker to v29.8.2 and Buildx to v0.37.2 by @github-actions[bot] in https://github.com/actions/runner/pull/4751
* Implement broker-listener WebSocket connectivity probes. by @TingluoHuang in https://github.com/actions/runner/pull/4753
* feat(runner): use node24 for internal operations by @salmanmkc in https://github.com/actions/runner/pull/4752
* Bump @typescript-eslint/parser from 8.70.1 to 8.71.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4754
* Bump @typescript-eslint/eslint-plugin from 8.70.1 to 8.71.0 in /src/Misc/expressionFunc/hashFiles by @dependabot[bot] in https://github.com/actions/runner/pull/4755

The runner's own Node usage (`hashFiles()`, `./run.sh --check`, and the service wrapper `runsvc.sh`) now uses Node 24 instead of Node 20 (#4752). To temporarily go back, set `ACTIONS_RUNNER_FORCED_INTERNAL_NODE_VERSION=node20` in the runner's environment (for example in its `.env` file). This covers `hashFiles()` and `--check` only.


**Full Changelog**: https://github.com/actions/runner/compare/v2.337.0...v2.338.0

_Note: Actions Runner follows a progressive release policy, so the latest release might not be available to your enterprise, organization, or repository yet.
To confirm which version of the Actions Runner you should expect, please view the download instructions for your enterprise, organization, or repository.
See https://docs.github.com/en/enterprise-cloud@latest/actions/hosting-your-own-runners/adding-self-hosted-runners_

## Windows x64

We recommend configuring the runner in a root folder of the Windows drive (e.g. "C:\actions-runner"). This will help avoid issues related to service identity folder permissions and long file path restrictions on Windows.

The following snipped needs to be run on `powershell`:

```powershell
# Create a folder under the drive root
mkdir \actions-runner ; cd \actions-runner
# Download the latest runner package
Invoke-WebRequest -Uri https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-win-x64-2.338.0.zip -OutFile actions-runner-win-x64-2.338.0.zip
# Extract the installer
Add-Type -AssemblyName System.IO.Compression.FileSystem ;
[System.IO.Compression.ZipFile]::ExtractToDirectory("$PWD\actions-runner-win-x64-2.338.0.zip", "$PWD")
```

## Windows arm64

We recommend configuring the runner in a root folder of the Windows drive (e.g. "C:\actions-runner"). This will help avoid issues related to service identity folder permissions and long file path restrictions on Windows.

The following snipped needs to be run on `powershell`:

```powershell
# Create a folder under the drive root
mkdir \actions-runner ; cd \actions-runner
# Download the latest runner package
Invoke-WebRequest -Uri https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-win-arm64-2.338.0.zip -OutFile actions-runner-win-arm64-2.338.0.zip
# Extract the installer
Add-Type -AssemblyName System.IO.Compression.FileSystem ;
[System.IO.Compression.ZipFile]::ExtractToDirectory("$PWD\actions-runner-win-arm64-2.338.0.zip", "$PWD")
```

## OSX x64

```bash
# Create a folder
mkdir actions-runner && cd actions-runner
# Download the latest runner package
curl -O -L https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-osx-x64-2.338.0.tar.gz
# Extract the installer
tar xzf ./actions-runner-osx-x64-2.338.0.tar.gz
```

## OSX arm64 (Apple silicon)

```bash
# Create a folder
mkdir actions-runner && cd actions-runner
# Download the latest runner package
curl -O -L https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-osx-arm64-2.338.0.tar.gz
# Extract the installer
tar xzf ./actions-runner-osx-arm64-2.338.0.tar.gz
```

## Linux x64

```bash
# Create a folder
mkdir actions-runner && cd actions-runner
# Download the latest runner package
curl -O -L https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-linux-x64-2.338.0.tar.gz
# Extract the installer
tar xzf ./actions-runner-linux-x64-2.338.0.tar.gz
```

## Linux arm64

```bash
# Create a folder
mkdir actions-runner && cd actions-runner
# Download the latest runner package
curl -O -L https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-linux-arm64-2.338.0.tar.gz
# Extract the installer
tar xzf ./actions-runner-linux-arm64-2.338.0.tar.gz
```

## Linux arm

```bash
# Create a folder
mkdir actions-runner && cd actions-runner
# Download the latest runner package
curl -O -L https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-linux-arm-2.338.0.tar.gz
# Extract the installer
tar xzf ./actions-runner-linux-arm-2.338.0.tar.gz
```

## Using your self hosted runner

For additional details about configuring, running, or shutting down the runner please check out our [product docs.](https://help.github.com/en/actions/automating-your-workflow-with-github-actions/adding-self-hosted-runners)

## SHA-256 Checksums

The SHA-256 checksums for the packages included in this build are shown below:

- actions-runner-win-x64-2.338.0.zip <!-- BEGIN SHA win-x64 -->f48e0750a21812bca5f82de5f7f5aeae71abee647fab5a582f1742d07eba455f<!-- END SHA win-x64 -->
- actions-runner-win-arm64-2.338.0.zip <!-- BEGIN SHA win-arm64 -->a57a4d29c0dcf02210a16b01906ed76cb8091b26d728c41232736c0f627a557e<!-- END SHA win-arm64 -->
- actions-runner-osx-x64-2.338.0.tar.gz <!-- BEGIN SHA osx-x64 -->dea7a58796ce215fc424a8b27c0fdd9b813fa5fc03d109a4a1d38131d6bfa1a3<!-- END SHA osx-x64 -->
- actions-runner-osx-arm64-2.338.0.tar.gz <!-- BEGIN SHA osx-arm64 -->df4cebda25c86a886ed204e49fee63f5c2e7cec5f447b5c98440a826bbdf9df2<!-- END SHA osx-arm64 -->
- actions-runner-linux-x64-2.338.0.tar.gz <!-- BEGIN SHA linux-x64 -->af4b794c1bc41d73d40535e3fe092a39f9679cd8d965954c2aca25a05ca41d32<!-- END SHA linux-x64 -->
- actions-runner-linux-arm64-2.338.0.tar.gz <!-- BEGIN SHA linux-arm64 -->628b4a7258487b80c1d3c221095a7ded349f5ae0175fd3dfab708d07428f041b<!-- END SHA linux-arm64 -->
- actions-runner-linux-arm-2.338.0.tar.gz <!-- BEGIN SHA linux-arm -->1d6a7688d2aca1a1fecd4df5d473e4aa4226c1f1785ff915cf84049665a3483f<!-- END SHA linux-arm -->
