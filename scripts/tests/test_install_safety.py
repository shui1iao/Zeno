import json
import os
import pathlib
import shutil
import stat
import subprocess
import tempfile
import textwrap
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
INSTALL_SH = ROOT / 'install.sh'
COMPOSE_YML = ROOT / 'docker-compose.yml'


class InstallSafetyTest(unittest.TestCase):
    def make_fake_bin(self, tempdir: pathlib.Path) -> pathlib.Path:
        fake_bin = tempdir / 'bin'
        fake_bin.mkdir()

        (fake_bin / 'id').write_text(textwrap.dedent(r'''#!/usr/bin/env bash
            if [ "${1:-}" = "-u" ]; then
              echo 0
              exit 0
            fi
            exec /usr/bin/id "$@"
        '''))
        (fake_bin / 'id').chmod(0o755)

        (fake_bin / 'chown').write_text(textwrap.dedent(r'''#!/usr/bin/env bash
            echo "chown $*" >> "${FAKE_DOCKER_LOG:?}"
            exit 0
        '''))
        (fake_bin / 'chown').chmod(0o755)

        (fake_bin / 'df').write_text(textwrap.dedent(r'''#!/usr/bin/env bash
            echo "df $*" >> "${FAKE_DOCKER_LOG:?}"
            if [ -n "${FAKE_AVAILABLE_KB:-}" ]; then
              printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
              printf 'fakefs 1000000 0 %s 0%% /\n' "$FAKE_AVAILABLE_KB"
              exit 0
            fi
            exec /bin/df "$@"
        '''))
        (fake_bin / 'df').chmod(0o755)

        (fake_bin / 'cp').write_text(textwrap.dedent(r'''#!/usr/bin/env bash
            echo "cp $*" >> "${FAKE_DOCKER_LOG:?}"
            if [ "${FAIL_STAGE:-}" = "backup_copy" ] && [[ "$*" == *"/.partial-install-"* ]]; then
              mkdir -p "${FAKE_DOCKER_STATE:?}"
              touch "${FAKE_DOCKER_STATE:?}/failed"
              exit 77
            fi
            if [ "${FAIL_RESTORE:-}" = "stage_copy" ] && [[ "$*" == *"/.staging/restore-"* ]]; then
              mkdir -p "${FAKE_DOCKER_STATE:?}"
              exit 80
            fi
            exec /bin/cp "$@"
        '''))
        (fake_bin / 'cp').chmod(0o755)

        docker = fake_bin / 'docker'
        docker.write_text(textwrap.dedent(r'''#!/usr/bin/env bash
            set -euo pipefail
            log=${FAKE_DOCKER_LOG:?}
            state=${FAKE_DOCKER_STATE:?}
            mkdir -p "$state"
            echo "docker $*" >> "$log"
            failed() { [ -f "$state/failed" ]; }
            if [ "${1:-}" = "compose" ]; then
              shift
              compose_file=""
              while [ "$#" -gt 0 ]; do
                case "$1" in
                  --env-file) shift 2 ;;
                  -f) compose_file="$2"; shift 2 ;;
                  version) echo "compose:version" >> "$log"; exit 0 ;;
                  *) break ;;
                esac
              done
              cmd=${1:-}
              shift || true
              echo "compose:${cmd}:${compose_file}" >> "$log"
              case "$cmd" in
                config)
                  exit 0
                  ;;
                pull)
                  if [ "${FAIL_STAGE:-}" = "pull" ]; then exit 71; fi
                  exit 0
                  ;;
                stop)
                  if failed && [ "${FAIL_RESTORE:-}" = "stop" ]; then exit 72; fi
                  if ! failed && [ "${FAIL_STAGE:-}" = "stop" ]; then touch "$state/failed"; exit 73; fi
                  touch "$state/stopped"
                  exit 0
                  ;;
                up)
                  if failed; then
                    if [ "${FAIL_RESTORE:-}" = "up" ]; then exit 74; fi
                    touch "$state/restored"
                    exit 0
                  fi
                  if [ "${FAIL_STAGE:-}" = "up" ]; then touch "$state/failed"; exit 75; fi
                  touch "$state/new_up"
                  exit 0
                  ;;
                ps)
                  if [ "${1:-}" = "-q" ]; then printf '%s\n' fake-container-id; fi
                  exit 0
                  ;;
                logs)
                  exit 0
                  ;;
                down)
                  touch "$state/down"
                  exit 0
                  ;;
              esac
              echo "unexpected compose command: $cmd" >&2
              exit 64
            fi
            if [ "${1:-}" = "run" ]; then
              echo "docker:run:$*" >> "$log"
              data_mount=""
              want_volume="0"
              for arg in "$@"; do
                if [ "$want_volume" = "1" ]; then
                  case "$arg" in
                    *:/data|*:/data:*) data_mount="${arg%%:/data*}" ;;
                  esac
                  want_volume="0"
                  continue
                fi
                if [ "$arg" = "-v" ]; then
                  want_volume="1"
                fi
              done
              if [ "${FAIL_ON_BACKUP_DB_MOUNT:-}" = "1" ] && [[ "$data_mount" == *"/backups/"* ]]; then
                exit 81
              fi
              if [ "${MUTATE_CHECK_DB:-}" = "1" ] && [ -n "$data_mount" ] && [ -f "$data_mount/zeno.db" ]; then
                printf '\nmutated-by-check\n' >> "$data_mount/zeno.db"
              fi
              if failed; then
                if [ "${FAIL_RESTORE:-}" = "quick_check" ]; then exit 78; fi
              else
                if [ "${FAIL_STAGE:-}" = "backup_quick_check" ] && [[ "$data_mount" == *"/.staging/check-"* ]]; then touch "$state/failed"; exit 79; fi
                if [ "${FAIL_STAGE:-}" = "quick_check" ] && [ "$data_mount" = "${ZENO_INSTALL_DIR:?}/data" ]; then
                  if [ "${CORRUPT_BACKUP_BEFORE_RESTORE:-}" = "1" ]; then
                    rm -f "${ZENO_INSTALL_DIR:?}"/backups/install-*/.zeno-backup-complete
                  fi
                  touch "$state/failed"
                  exit 76
                fi
              fi
              exit 0
            fi
            if [ "${1:-}" = "inspect" ]; then
              if [[ "$*" == *"{{.Image}}"* ]]; then
                printf '%s\n' 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
              fi
              exit 0
            fi
            if [ "${1:-}" = "image" ]; then
              case "${2:-}" in
                inspect)
                  if [[ "$*" == *"{{.Id}}"* ]]; then
                    printf '%s\n' 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
                  elif [[ "$*" == *"RepoDigests"* ]]; then
                    if [ "${FAKE_OFFICIAL_IMAGE:-}" = "1" ]; then
                      printf '%s\n' 'ghcr.io/shui1iao/zeno@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
                    else
                      printf '%s\n' 'registry.example/zeno@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
                    fi
                  elif [[ "$*" == *"org.opencontainers.image.source"* ]]; then
                    printf '%s\n' 'https://github.com/shui1iao/Zeno'
                  elif [[ "$*" == *"org.opencontainers.image.revision"* ]]; then
                    printf '%s\n' 'cccccccccccccccccccccccccccccccccccccccc'
                  elif [[ "$*" == *"org.opencontainers.image.version"* ]]; then
                    printf '%s\n' '0.6.1'
                  fi
                  exit 0
                  ;;
                tag)
                  printf '%s\n' "${4:-}" > "$state/rollback_tag"
                  exit 0
                  ;;
                rm)
                  exit 0
                  ;;
              esac
            fi
            echo "unexpected docker command: $*" >&2
            exit 64
        '''))
        docker.chmod(0o755)

        curl = fake_bin / 'curl'
        curl.write_text(textwrap.dedent(r'''#!/usr/bin/env bash
            set -euo pipefail
            log=${FAKE_DOCKER_LOG:?}
            state=${FAKE_DOCKER_STATE:?}
            echo "curl $*" >> "$log"
            if [[ "$*" == *"/ready"* ]]; then
              if [ -f "$state/restored" ] && [ "${FAIL_RESTORE:-}" = "ready" ]; then exit 22; fi
              if [ "${FAIL_STAGE:-}" = "ready" ] && [ ! -f "$state/restored" ]; then
                count_file="$state/ready_count"
                count=0
                [ -f "$count_file" ] && count=$(cat "$count_file")
                count=$((count + 1))
                printf '%s\n' "$count" > "$count_file"
                if [ "$count" -ge 60 ]; then touch "$state/failed"; fi
                exit 22
              fi
              exit 0
            fi
            exit 0
        '''))
        curl.chmod(0o755)

        sleep = fake_bin / 'sleep'
        sleep.write_text('#!/usr/bin/env bash\nexit 0\n')
        sleep.chmod(0o755)

        timeout = fake_bin / 'timeout'
        timeout.write_text(textwrap.dedent(r'''#!/usr/bin/env bash
            set -euo pipefail
            echo "timeout $*" >> "${FAKE_DOCKER_LOG:?}"
            shift 2
            if [ "${FAIL_STAGE:-}" = "db_timeout" ] && [[ "$*" == *"${ZENO_INSTALL_DIR:?}/data:/data"* ]]; then
              mkdir -p "${FAKE_DOCKER_STATE:?}"
              touch "${FAKE_DOCKER_STATE:?}/failed"
              exit 124
            fi
            exec "$@"
        '''))
        timeout.chmod(0o755)

        openssl = fake_bin / 'openssl'
        openssl.write_text('#!/usr/bin/env bash\nprintf %s fake-secret-token\n')
        openssl.chmod(0o755)
        return fake_bin

    def make_existing_install(self, install_dir: pathlib.Path) -> None:
        (install_dir / 'data').mkdir(parents=True)
        (install_dir / 'secrets').mkdir()
        (install_dir / '.env').write_text('ZENO_IMAGE=registry.example/zeno:old\nZENO_CONTAINER_NAME=old-zeno\nZENO_HOST_PORT=18981\nTZ=Asia/Shanghai\n')
        (install_dir / 'docker-compose.yml').write_text('services:\n  zeno:\n    image: registry.example/zeno:old\n')
        (install_dir / 'data' / 'zeno.db').write_text('old-db')
        (install_dir / 'data' / 'zeno.db-wal').write_text('old-wal')
        (install_dir / 'data' / 'zeno.db-shm').write_text('old-shm')
        (install_dir / 'secrets' / 'zeno_admin_token').write_text('old-admin')
        (install_dir / 'secrets' / 'zeno_agent_token').write_text('old-agent')

    def backup_source_size_bytes(self, install_dir: pathlib.Path) -> int:
        total = 0
        for name in ('.env', 'docker-compose.yml', 'data', 'secrets'):
            path = install_dir / name
            if path.exists():
                blocks = int(subprocess.check_output(['du', '-sk', str(path)], text=True).split()[0])
                total += blocks * 1024
        return total

    def manifest_verifies(self, backup_dir: pathlib.Path) -> bool:
        result = subprocess.run(['sha256sum', '-c', 'MANIFEST.sha256'], cwd=backup_dir, text=True, capture_output=True)
        return result.returncode == 0

    def run_install(self, tempdir: pathlib.Path, stage: str = '', restore: str = '', extra_env=None, setup=None, prepare_env=None, existing=True):
        install_dir = tempdir / 'zeno-install'
        if existing:
            self.make_existing_install(install_dir)
        if setup:
            setup(install_dir)
        fake_bin = self.make_fake_bin(tempdir)
        log = tempdir / 'docker.log'
        state = tempdir / 'state'
        env = os.environ.copy()
        env.update({
            'PATH': f'{fake_bin}:{env["PATH"]}',
            'FAKE_DOCKER_LOG': str(log),
            'FAKE_DOCKER_STATE': str(state),
            'ZENO_INSTALL_DIR': str(install_dir),
            'ZENO_IMAGE': 'registry.example/zeno:new',
            'ZENO_HOST_PORT': '18982',
            'ZENO_CONTAINER_NAME': 'new-zeno',
            'TZ': 'Asia/Shanghai',
            'ZENO_MIN_FREE_BYTES': '1024',
        })
        if stage:
            env['FAIL_STAGE'] = stage
        if restore:
            env['FAIL_RESTORE'] = restore
        if extra_env:
            env.update(extra_env)
        if prepare_env:
            prepare_env(install_dir, env)
        result = subprocess.run(['bash', str(INSTALL_SH)], env=env, text=True, capture_output=True, timeout=20)
        log_text = log.read_text() if log.exists() else ''
        return result, install_dir, log_text

    def latest_backup(self, install_dir: pathlib.Path) -> pathlib.Path:
        backup_file = install_dir / '.last-install-backup'
        self.assertTrue(backup_file.exists(), 'last backup pointer should exist')
        backup_dir = pathlib.Path(backup_file.read_text().strip())
        self.assertTrue(backup_dir.exists(), f'backup dir missing: {backup_dir}')
        return backup_dir

    def test_preflight_config_pull_and_disk_check_happen_before_first_stop(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(pathlib.Path(td))
            env_text = (install_dir / '.env').read_text()
        self.assertEqual(result.returncode, 0, result.stderr)
        lines = log_text.splitlines()
        df_index = next(i for i, line in enumerate(lines) if line.startswith('df '))
        config_index = next(i for i, line in enumerate(lines) if 'compose:config:' in line and '/.staging/' in line)
        pull_index = next(i for i, line in enumerate(lines) if 'compose:pull:' in line and '/.staging/' in line)
        stop_index = next(i for i, line in enumerate(lines) if 'compose:stop:' in line)
        self.assertLess(df_index, stop_index)
        self.assertLess(config_index, stop_index)
        self.assertLess(pull_index, stop_index)
        self.assertIn('ZENO_IMAGE=registry.example/zeno@sha256:', env_text)
        self.assertIn('ZENO_UPDATE_IMAGE=registry.example/zeno:new', env_text)

    def test_success_creates_complete_backup_marker_manifest_and_private_permissions(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(pathlib.Path(td))
            backup_dir = self.latest_backup(install_dir)
            dirs = {name: stat.S_IMODE((install_dir / name).stat().st_mode) for name in ('data', 'secrets', 'backups', 'builds')}
            secret_modes = {p.name: stat.S_IMODE(p.stat().st_mode) for p in (install_dir / 'secrets').iterdir() if p.is_file()}
            authority_ring = json.loads((install_dir / 'secrets' / 'zeno_notification_authority_keyring.json').read_text())
            credential_ring = json.loads((install_dir / 'secrets' / 'zeno_notification_credential_keyring.json').read_text())
            authority_secret = (install_dir / 'secrets' / 'zeno_notification_authority').read_text().strip()
            credential_secret = (install_dir / 'secrets' / 'zeno_notification_credential_key').read_text().strip()
            self.assertTrue((backup_dir / '.zeno-backup-complete').exists())
            self.assertTrue((backup_dir / 'MANIFEST.sha256').exists())
            self.assertTrue((backup_dir / 'secrets' / 'zeno_notification_credential_key').exists())
            self.assertFalse(list((install_dir / 'backups').glob('.partial-install-*')))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(dirs, {'data': 0o700, 'secrets': 0o750, 'backups': 0o700, 'builds': 0o700})
        self.assertTrue(secret_modes)
        self.assertTrue(all(mode == 0o640 for mode in secret_modes.values()))
        self.assertEqual(authority_ring, {'active_key_id': 'primary', 'keys': {'primary': authority_secret}})
        self.assertEqual(credential_ring, {'active_key_id': 'primary', 'keys': {'primary': credential_secret}})
        self.assertIn('chown 10001:10001', log_text)
        self.assertIn('chown 0:10001', log_text)
        self.assertIn('chown 0:0', log_text)

    def test_backup_quick_check_uses_scratch_so_formal_backup_manifest_survives_db_writes(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(pathlib.Path(td), extra_env={'MUTATE_CHECK_DB': '1'})
            backup_dir = self.latest_backup(install_dir)
            manifest_ok = self.manifest_verifies(backup_dir)
            backup_db = (backup_dir / 'data' / 'zeno.db').read_text()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(manifest_ok)
        self.assertEqual(backup_db, 'old-db')

    def test_backup_excludes_pre_vacuum_snapshots_and_persists_retention(self):
        def seed_backup_policy(install_dir: pathlib.Path) -> None:
            with (install_dir / '.env').open('a') as env_file:
                env_file.write('ZENO_BACKUP_KEEP_COUNT=1\n')
            (install_dir / 'data' / 'zeno.db.prevacuum-20260731-213739').write_text('stale-offline-snapshot')
            (install_dir / 'data' / 'runtime-metadata').write_text('keep-runtime-data')

        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(pathlib.Path(td), setup=seed_backup_policy)
            backup_dir = self.latest_backup(install_dir)
            backup_files = {path.name for path in (backup_dir / 'data').iterdir()}
            env_text = (install_dir / '.env').read_text()
            manifest_ok = self.manifest_verifies(backup_dir)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('zeno.db', backup_files)
        self.assertIn('runtime-metadata', backup_files)
        self.assertNotIn('zeno.db.prevacuum-20260731-213739', backup_files)
        self.assertIn('ZENO_BACKUP_KEEP_COUNT=1', env_text)
        self.assertTrue(manifest_ok)

    def test_legacy_root_private_runtime_dirs_upgrade_without_mounting_backup_into_check_container(self):
        def make_legacy_private(install_dir: pathlib.Path) -> None:
            os.chmod(install_dir / 'data', 0o700)
            os.chmod(install_dir / 'secrets', 0o700)
            for path in (install_dir / 'data').iterdir():
                os.chmod(path, 0o600)
            for path in (install_dir / 'secrets').iterdir():
                os.chmod(path, 0o600)

        with tempfile.TemporaryDirectory() as td:
            result, _, log_text = self.run_install(
                pathlib.Path(td),
                setup=make_legacy_private,
                extra_env={'FAIL_ON_BACKUP_DB_MOUNT': '1'},
            )
            run_lines = [line for line in log_text.splitlines() if line.startswith('docker:run:')]
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(run_lines)
        self.assertTrue(all('/backups/' not in line for line in run_lines), run_lines)

    def test_backup_copy_failure_does_not_publish_or_restore_partial_backup(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(pathlib.Path(td), stage='backup_copy')
            env_text = (install_dir / '.env').read_text()
            last_backup_exists = (install_dir / '.last-install-backup').exists()
            complete_backups = list((install_dir / 'backups').glob('install-*'))
            partial_backups = list((install_dir / 'backups').glob('.partial-install-*'))
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('ZENO_IMAGE=registry.example/zeno:old', env_text)
        self.assertFalse(last_backup_exists)
        self.assertFalse(complete_backups)
        self.assertFalse(partial_backups)
        self.assertIn('未完成备份不会用于恢复', result.stderr)
        self.assertNotIn('已恢复旧版本', result.stderr)
        self.assertIn('compose:up:', log_text)

    def test_restore_preserves_failed_state_and_complete_backup(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(pathlib.Path(td), stage='up')
            env_text = (install_dir / '.env').read_text()
            backup_dir = self.latest_backup(install_dir)
            failed_state = pathlib.Path((install_dir / '.last-failed-install-state').read_text().strip())
            failed_env = (failed_state / '.env').read_text()
            backup_info = (backup_dir / 'BACKUP_INFO').read_text()
            self.assertTrue((backup_dir / '.zeno-backup-complete').exists())
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('已恢复旧版本', result.stderr)
        self.assertIn('ZENO_IMAGE=zeno-rollback:', env_text)
        self.assertIn('ZENO_UPDATE_IMAGE=registry.example/zeno:old', env_text)
        self.assertIn('rollback_image_id=sha256:aaaaaaaa', backup_info)
        self.assertIn('rollback_image_ref=zeno-rollback:', backup_info)
        self.assertIn('ZENO_IMAGE=registry.example/zeno@sha256:', failed_env)
        self.assertIn('ZENO_UPDATE_IMAGE=registry.example/zeno:new', failed_env)

    def test_fresh_install_ready_failure_stops_container_and_quarantines_scene(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(pathlib.Path(td), stage='ready', existing=False)
            failed_pointer = install_dir / '.last-failed-install-state'
            self.assertTrue(failed_pointer.exists())
            failed_state = pathlib.Path(failed_pointer.read_text().strip())
            self.assertTrue((failed_state / '.env').exists())
            self.assertTrue((failed_state / 'docker-compose.yml').exists())
            self.assertFalse((install_dir / '.env').exists())
            self.assertFalse((install_dir / 'data').exists())
            self.assertFalse((install_dir / 'secrets').exists())
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('首次安装失败现场已隔离', result.stderr)
        self.assertIn('compose:down:', log_text)

    def test_secret_symlink_is_rejected_before_stopping_existing_service(self):
        external = {}

        def install_symlink(install_dir: pathlib.Path) -> None:
            target = install_dir.parent / 'external-secret-target'
            target.write_text('do-not-touch')
            (install_dir / 'secrets' / 'zeno_admin_token').unlink()
            (install_dir / 'secrets' / 'zeno_admin_token').symlink_to(target)
            external['target'] = target

        with tempfile.TemporaryDirectory() as td:
            result, _, log_text = self.run_install(pathlib.Path(td), setup=install_symlink)
            value = external['target'].read_text()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('包含符号链接', result.stderr)
        self.assertEqual(value, 'do-not-touch')
        self.assertNotIn('compose:stop:', log_text)

    def test_data_symlink_is_rejected_before_stopping_existing_service(self):
        external = {}

        def install_symlink(install_dir: pathlib.Path) -> None:
            target = install_dir.parent / 'external-data-target'
            target.mkdir()
            (target / 'zeno.db').write_text('external-db')
            shutil.rmtree(install_dir / 'data')
            (install_dir / 'data').symlink_to(target, target_is_directory=True)
            external['target'] = target

        with tempfile.TemporaryDirectory() as td:
            result, _, log_text = self.run_install(pathlib.Path(td), setup=install_symlink)
            value = (external['target'] / 'zeno.db').read_text()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('拒绝符号链接目录', result.stderr)
        self.assertEqual(value, 'external-db')
        self.assertNotIn('compose:stop:', log_text)
        self.assertNotIn('compose:pull:', log_text)

    def test_database_check_timeout_restores_immutable_old_image(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(pathlib.Path(td), stage='db_timeout')
            env_text = (install_dir / '.env').read_text()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('已恢复旧版本', result.stderr)
        self.assertIn('ZENO_IMAGE=zeno-rollback:', env_text)

    def test_database_check_timeout_defaults_is_persisted_and_passed_to_controller(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(pathlib.Path(td))
            env_text = (install_dir / '.env').read_text()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('ZENO_DB_CHECK_TIMEOUT=10m', env_text)
        self.assertIn('timeout --foreground 10m docker run --rm', log_text)
        self.assertIn('-check-db-timeout 10m', log_text)

    def test_database_check_timeout_accepts_custom_duration(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(
                pathlib.Path(td), extra_env={'ZENO_DB_CHECK_TIMEOUT': '20m'})
            env_text = (install_dir / '.env').read_text()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('ZENO_DB_CHECK_TIMEOUT=20m', env_text)
        self.assertIn('-check-db-timeout 20m', log_text)

    def test_database_check_timeout_rejects_zero_negative_and_excessive_values(self):
        for value in ('0s', '-1s', '25h', '1441m', '86401s'):
            with self.subTest(value=value), tempfile.TemporaryDirectory() as td:
                result, _, log_text = self.run_install(
                    pathlib.Path(td), extra_env={'ZENO_DB_CHECK_TIMEOUT': value})
            self.assertNotEqual(result.returncode, 0, result.stdout)
            self.assertIn('ZENO_DB_CHECK_TIMEOUT', result.stderr)
            self.assertNotIn('compose:stop:', log_text)

    def test_restore_quick_check_uses_scratch_so_backup_manifest_survives_db_writes(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(pathlib.Path(td), stage='up', extra_env={'MUTATE_CHECK_DB': '1'})
            backup_dir = self.latest_backup(install_dir)
            manifest_ok = self.manifest_verifies(backup_dir)
            backup_db = (backup_dir / 'data' / 'zeno.db').read_text()
            restored_db = (install_dir / 'data' / 'zeno.db').read_text()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('已恢复旧版本', result.stderr)
        self.assertTrue(manifest_ok)
        self.assertEqual(backup_db, 'old-db')
        self.assertEqual(restored_db, 'old-db')

    def test_restore_refuses_backup_that_fails_restore_quick_check_before_overwriting_scene(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(pathlib.Path(td), stage='up', restore='quick_check')
            env_text = (install_dir / '.env').read_text()
            backup_dir = self.latest_backup(install_dir)
            self.assertTrue((backup_dir / '.zeno-backup-complete').exists())
            failed_state_exists = (install_dir / '.last-failed-install-state').exists()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('自动恢复失败', result.stderr)
        self.assertIn('ZENO_IMAGE=registry.example/zeno@sha256:', env_text)
        self.assertIn('ZENO_UPDATE_IMAGE=registry.example/zeno:new', env_text)
        self.assertFalse(failed_state_exists)

    def test_restore_stage_copy_enospc_does_not_move_current_install_first(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(pathlib.Path(td), stage='up', restore='stage_copy')
            env_text = (install_dir / '.env').read_text()
            data_exists = (install_dir / 'data' / 'zeno.db').exists()
            failed_state_exists = (install_dir / '.last-failed-install-state').exists()
            stop_count = sum(1 for line in log_text.splitlines() if 'compose:stop:' in line)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('自动恢复失败', result.stderr)
        self.assertIn('ZENO_IMAGE=registry.example/zeno@sha256:', env_text)
        self.assertIn('ZENO_UPDATE_IMAGE=registry.example/zeno:new', env_text)
        self.assertTrue(data_exists)
        self.assertFalse(failed_state_exists)
        self.assertEqual(stop_count, 1)

    def test_corrupt_backup_marker_is_not_used_for_restore(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(
                pathlib.Path(td),
                stage='quick_check',
                extra_env={'CORRUPT_BACKUP_BEFORE_RESTORE': '1'},
            )
            env_text = (install_dir / '.env').read_text()
            backup_dirs = list((install_dir / 'backups').glob('install-*'))
            marker_exists = bool(backup_dirs and (backup_dirs[0] / '.zeno-backup-complete').exists())
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('自动恢复失败', result.stderr)
        self.assertIn('ZENO_IMAGE=registry.example/zeno:old', env_text)
        self.assertTrue(backup_dirs)
        self.assertFalse(marker_exists)

    def test_preflight_disk_space_failure_happens_before_stop(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(
                pathlib.Path(td),
                extra_env={'FAKE_AVAILABLE_KB': '1', 'ZENO_MIN_FREE_BYTES': '1048576'},
            )
            env_text = (install_dir / '.env').read_text()
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('磁盘空间不足', result.stderr)
        self.assertIn('ZENO_IMAGE=registry.example/zeno:old', env_text)
        self.assertNotIn('compose:stop:', log_text)

    def test_preflight_disk_space_includes_backup_and_restore_staging_copies(self):
        boundary = {}

        def set_between_old_and_new_requirement(install_dir: pathlib.Path, env: dict) -> None:
            source_bytes = self.backup_source_size_bytes(install_dir)
            min_free = 1024
            available_bytes = source_bytes * 2 + min_free + 1024
            boundary['source_bytes'] = source_bytes
            boundary['available_bytes'] = available_bytes
            env['ZENO_MIN_FREE_BYTES'] = str(min_free)
            env['FAKE_AVAILABLE_KB'] = str(available_bytes // 1024)

        with tempfile.TemporaryDirectory() as td:
            result, install_dir, log_text = self.run_install(pathlib.Path(td), prepare_env=set_between_old_and_new_requirement)
            env_text = (install_dir / '.env').read_text()
        self.assertGreaterEqual(boundary['available_bytes'], boundary['source_bytes'] * 2 + 1024)
        self.assertLess(boundary['available_bytes'], boundary['source_bytes'] * 3 + 1024)
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn('磁盘空间不足', result.stderr)
        self.assertIn('ZENO_IMAGE=registry.example/zeno:old', env_text)
        self.assertNotIn('compose:stop:', log_text)

    def test_lifecycle_rotation_is_limited_and_does_not_delete_business_data(self):
        def seed_lifecycle(install_dir: pathlib.Path) -> None:
            (install_dir / 'data' / 'business.txt').write_text('keep-me')
            backups = install_dir / 'backups'
            builds = install_dir / 'builds'
            backups.mkdir()
            builds.mkdir()
            base = 1_700_000_000
            for i in range(8):
                complete = backups / f'install-old-{i}'
                complete.mkdir()
                (complete / '.zeno-backup-complete').write_text('ok')
                os.utime(complete, (base + i, base + i))
                failed = backups / f'failed-old-{i}'
                failed.mkdir()
                os.utime(failed, (base + i, base + i))
                release = builds / f'release-old-{i}'
                release.mkdir()
                os.utime(release, (base + i, base + i))
                archive = builds / f'zeno-old-{i}.tar.gz'
                archive.write_text('archive')
                os.utime(archive, (base + i, base + i))

        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(pathlib.Path(td), setup=seed_lifecycle)
            install_backups = list((install_dir / 'backups').glob('install-*'))
            failed_states = list((install_dir / 'backups').glob('failed-*'))
            release_dirs = list((install_dir / 'builds').glob('release-*'))
            archives = list((install_dir / 'builds').glob('zeno-*.tar.gz'))
            business = (install_dir / 'data' / 'business.txt').read_text()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertLessEqual(len(install_backups), 5)
        self.assertLessEqual(len(failed_states), 3)
        self.assertLessEqual(len(release_dirs), 3)
        self.assertLessEqual(len(archives), 3)
        self.assertEqual(business, 'keep-me')

    def test_runtime_hardening_and_agent_install_route_are_static_guards(self):
        compose = COMPOSE_YML.read_text()
        script = INSTALL_SH.read_text()
        for text in (compose, script):
            self.assertIn('read_only: true', text)
            self.assertIn('cap_drop:', text)
            self.assertIn('ALL', text)
            self.assertIn('no-new-privileges:true', text)
            self.assertIn('127.0.0.1:${ZENO_HOST_PORT:-18980}:18980', text)
        self.assertIn('https://zeno.shuijiao.de/agent/install.sh', script)
        self.assertIn('https://zeno.shuijiao.de/agent/install.ps1', script)
        self.assertIn('ZENO_NOTIFICATION_AUTHORITY_KEYRING_FILE: /run/secrets/zeno_notification_authority_keyring.json', compose)
        self.assertIn('ZENO_NOTIFICATION_CREDENTIAL_KEYRING_FILE: /run/secrets/zeno_notification_credential_keyring.json', compose)
        self.assertIn('ZENO_NOTIFICATION_AUTHORITY_KEYRING_FILE: /run/secrets/zeno_notification_authority_keyring.json', script)
        self.assertIn('ZENO_NOTIFICATION_CREDENTIAL_KEYRING_FILE: /run/secrets/zeno_notification_credential_keyring.json', script)
        self.assertIn('ZENO_NOTIFICATIONS_DISABLED=${NOTIFICATIONS_DISABLED}', script)
        self.assertIn('validate_bool "$NOTIFICATIONS_DISABLED" ZENO_NOTIFICATIONS_DISABLED', script)
        self.assertIn('ensure_single_keyring_file', script)
        self.assertNotIn('ZENO_NOTIFICATION_CREDENTIAL_KEY=', compose)
        self.assertNotIn('ZENO_NOTIFICATION_CREDENTIAL_KEY=', script)
        self.assertNotIn('raw.githubusercontent.com/${REPO}/main/install.sh', script)

    def test_script_uses_complete_markers_trap_staging_and_no_true_masking(self):
        script = INSTALL_SH.read_text()
        self.assertIn('trap rollback_on_error ERR', script)
        self.assertIn('.zeno-backup-complete', script)
        self.assertIn('MANIFEST.sha256', script)
        self.assertIn('verify_backup_for_restore', script)
        self.assertIn('preflight_disk_space', script)
        self.assertIn('preserve_failed_state', script)
        self.assertIn('STAGING_DIR=', script)
        self.assertIn('atomic_install_file', script)
        self.assertIn('verify_official_image_attestation', script)
        self.assertIn('gh_sha="762569efe785082b7d1feb06995efece1a9cecce16da8503ac6fdbcbea04085b"', script)
        self.assertIn('^v?[0-9]+\\.[0-9]+\\.[0-9]+', script)
        self.assertIn('TARGET_VERSION_LABEL="${version_label#v}"', script)
        self.assertIn('https://ghcr.io/token?scope=repository%3Ashui1iao%2Fzeno%3Apull', script)
        self.assertIn('attestation verify', script)
        self.assertIn('GH_TOKEN=${verifier_token}', script)
        # 用户名迁移后，旧 tag 的 provenance 证书 owner 仍是 shuijiao1，
        # 校验必须对新旧 owner 都尝试一次，否则旧版本无法安装或回滚。
        self.assertIn('LEGACY_REPO="shuijiao1/Zeno"', script)
        self.assertIn('for repo_candidate in "$REPO" "$LEGACY_REPO"', script)
        self.assertIn('--bundle-from-oci', script)
        self.assertNotIn('|| true', script)

    def test_release_workflow_publishes_github_signed_image_attestation(self):
        workflow = (ROOT / '.github' / 'workflows' / 'docker.yml').read_text()
        ci_workflow = (ROOT / '.github' / 'workflows' / 'ci.yml').read_text()
        dockerfile = (ROOT / 'Dockerfile').read_text()
        policy = (ROOT / 'scripts' / 'check-release-policy.sh').read_text()
        self.assertIn('actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8', workflow)
        self.assertIn('subject-digest: ${{ steps.build.outputs.digest }}', workflow)
        self.assertIn('push-to-registry: true', workflow)
        self.assertIn('scripts/check-release-policy.sh', workflow)
        self.assertIn('release already exists', workflow)
        self.assertIn('cannot verify release uniqueness', workflow)
        self.assertIn('release tag is not strict SemVer', policy)
        self.assertIn('does not match tag', policy)
        self.assertIn('not greater than existing SemVer', policy)
        self.assertIn('platforms: linux/amd64,linux/arm64,linux/arm/v6', workflow)
        self.assertIn('docker/setup-qemu-action@96fe6ef7f33517b61c61be40b68a1882f3264fb8', workflow)
        self.assertIn('docker/build-push-action@53b7df96c91f9c12dcc8a07bcb9ccacbed38856a', workflow)
        self.assertIn('actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1', workflow)
        self.assertIn('bash scripts/wait-for-ci.sh "$GITHUB_REPOSITORY" "$GITHUB_SHA" main', workflow)
        self.assertIn('needs: [release-policy, verify-ci]', workflow)
        self.assertIn('actions: read', workflow)
        self.assertNotIn('actions/setup-go@', workflow)
        self.assertNotIn('actions/setup-node@', workflow)
        self.assertIn('actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e', ci_workflow)
        self.assertIn("go-version: '1.26.5'", ci_workflow)
        self.assertIn('actions/setup-node@820762786026740c76f36085b0efc47a31fe5020', ci_workflow)
        self.assertIn("node-version: '26.7.0'", ci_workflow)
        self.assertIn('golang/govulncheck-action@032d45514ae346b1db93c04b0c90b841c370344f', ci_workflow)
        self.assertNotIn("tags: ['v*']", ci_workflow)
        self.assertNotIn('docker/setup-qemu-action@68827325e0b33c7199eb31dd4e31fbe9023e06e3', workflow)
        self.assertNotIn('docker/build-push-action@10e90e3645eae34f1e60eeb005ba3a3d33f178e8', workflow)
        self.assertNotIn('golang/govulncheck-action@b625fbe08f3bccbe446d94fbf87fcc875a4f50ee', ci_workflow)
        self.assertIn('node:26.7.0-bookworm-slim@sha256:cd565714d4da3e84bfd341e31448f81d47c6362198f152345297c9c1154e6341', dockerfile)
        self.assertIn('golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd', dockerfile)
        self.assertIn('ARG TARGETVARIANT', dockerfile)
        self.assertIn('export GOARM=6', dockerfile)
        self.assertNotIn('ARG TARGETARCH=amd64', dockerfile)

    def test_official_image_can_only_skip_attestation_with_explicit_override(self):
        with tempfile.TemporaryDirectory() as td:
            result, install_dir, _ = self.run_install(
                pathlib.Path(td),
                extra_env={
                    'ZENO_IMAGE': 'ghcr.io/shui1iao/zeno:v0.6.1',
                    'ZENO_VERIFY_ATTESTATION': 'false',
                    'FAKE_OFFICIAL_IMAGE': '1',
                },
            )
            env_text = (install_dir / '.env').read_text()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('已显式关闭官方镜像 provenance 验证', result.stderr)
        self.assertIn('ZENO_IMAGE=ghcr.io/shui1iao/zeno@sha256:', env_text)
        self.assertIn('ZENO_UPDATE_IMAGE=ghcr.io/shui1iao/zeno:v0.6.1', env_text)

    def test_dockerignore_excludes_secrets_and_sqlite_but_keeps_examples(self):
        dockerignore = (ROOT / '.dockerignore').read_text()
        for pattern in ('.env', '**/.env', 'secrets/*', '**/secrets/**/*', '*.pem', '**/*.key', '*.db', '*.db-wal', '*.db-shm', '*-wal', '*-shm'):
            self.assertIn(pattern, dockerignore)
        for pattern in ('!.env.example', '!**/.env.example', '!*.pem.example', '!**/*.key.example', '!secrets/*.example', '!**/secrets/**/*.example'):
            self.assertIn(pattern, dockerignore)


if __name__ == '__main__':
    unittest.main()
