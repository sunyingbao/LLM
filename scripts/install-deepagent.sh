#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_dir="${repo_root}/.eino-cli/bin"
install_dir="${DEEPAGENT_INSTALL_DIR:-${HOME}/.local/bin}"

mkdir -p "${bin_dir}" "${install_dir}"

(cd "${repo_root}" && go build -o "${bin_dir}/deepagent" ./deepagent/cmd/deepagent)

cat >"${install_dir}/deepagent" <<EOF
#!/usr/bin/env bash
exec "${bin_dir}/deepagent" --root "${repo_root}" "\$@"
EOF
chmod +x "${install_dir}/deepagent"

case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *)
    echo "Installed deepagent to ${install_dir}/deepagent"
    echo "Add ${install_dir} to PATH, then run: deepagent"
    exit 0
    ;;
esac

echo "Installed deepagent to ${install_dir}/deepagent"
echo "Run: deepagent"
