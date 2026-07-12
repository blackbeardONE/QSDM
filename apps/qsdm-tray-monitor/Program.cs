using System.Diagnostics;
using System.Drawing;
using System.Globalization;
using System.Net.Http;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Threading;
using System.Windows.Forms;

namespace QsdmTrayMonitor;

internal static class Program
{
    [STAThread]
    private static void Main(string[] args)
    {
        using var mutex = new Mutex(true, @"Local\QSDMTrayMonitor", out var createdNew);
        if (!createdNew)
        {
            MessageBox.Show("QSDM Tray Monitor is already running.", "QSDM Tray Monitor",
                MessageBoxButtons.OK, MessageBoxIcon.Information);
            return;
        }

        ApplicationConfiguration.Initialize();
        Application.Run(new MonitorContext(args));
    }
}

internal sealed class MonitorContext : ApplicationContext
{
    private static readonly TimeSpan PollInterval = TimeSpan.FromSeconds(5);
    private static readonly TimeSpan ChainStallThreshold = TimeSpan.FromSeconds(90);
    private static readonly string[] ValidatorProcessPrefixes =
    [
        "qsdm-local-validator", "qsdm-sqlite"
    ];
    private static readonly string[] ValidatorProcessNames =
    [
        "qsdm-new", "qsdm"
    ];
    private static readonly string[] GatewayProcessNames =
    [
        "qsdm-home-gateway", "qsdm-home-gateway-hive", "qsdm-home-gateway-hive.new"
    ];

    private readonly NotifyIcon tray;
    private readonly System.Windows.Forms.Timer timer;
    private readonly HttpClient http;
    private readonly string qsdmRoot;
    private readonly string localRoot;
    private readonly string guiUrlFile;
    private readonly string adminGuiLauncher;
    private readonly string appDataDir;
    private readonly string statusPath;
    private readonly string logPath;
    private readonly string repositorySha;
    private readonly string minerRuntimePath;
    private readonly string minerStagedPath;
    private readonly string minerConfigPath;
    private readonly ToolStripMenuItem validatorItem = new("Validator: checking");
    private readonly ToolStripMenuItem networkItem = new("Network: checking");
    private readonly ToolStripMenuItem minerItem = new("Miner: checking");
    private readonly ToolStripMenuItem gatewayItem = new("Gateway: checking");
    private readonly ToolStripMenuItem attesterItem = new("Attester: checking");
    private readonly ToolStripMenuItem treasuryItem = new("Treasury: checking");
    private readonly ToolStripMenuItem watchdogItem = new("Watchdog: checking");
    private readonly ToolStripMenuItem guiItem = new("GUI: checking");
    private readonly ToolStripMenuItem exposureItem = new("Exposure: checking");
    private readonly ToolStripMenuItem lastCheckedItem = new("Last checked: -");
    private string lastStateKey = "";
    private DateTime? lastGatewayPublicOk;
    private int gatewayPublicFailures;
    private long? lastValidatorHeight;
    private DateTime lastHeightProgressAt = DateTime.Now;
    private DateTime minerVersionWriteUtc = DateTime.MinValue;
    private string minerVersionCache = "";
    private Icon? currentIcon;
    private bool checking;

    public MonitorContext(string[] args)
    {
        qsdmRoot = FindQsdmRoot(args);
        localRoot = Path.Combine(qsdmRoot, "source", ".cache", "local-validator");
        guiUrlFile = Path.Combine(localRoot, "local-gui-persist.url");
        adminGuiLauncher = Path.Combine(qsdmRoot, "scripts", "QSDM Admin GUI.cmd");
        appDataDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "QSDM-Tray-Monitor");
        statusPath = Path.Combine(appDataDir, "status.json");
        logPath = Path.Combine(appDataDir, "monitor.log");
        repositorySha = ReadRepositorySha(qsdmRoot);
        var workspaceRoot = Directory.GetParent(qsdmRoot)?.FullName ?? qsdmRoot;
        minerRuntimePath = Path.Combine(workspaceRoot, "Blackbeard", "qsdmminer.exe");
        minerStagedPath = minerRuntimePath + ".next";
        minerConfigPath = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".qsdm", "miner.toml");

        Directory.CreateDirectory(appDataDir);
        Environment.SetEnvironmentVariable("NO_PROXY", MergeNoProxy(Environment.GetEnvironmentVariable("NO_PROXY")));
        Environment.SetEnvironmentVariable("no_proxy", MergeNoProxy(Environment.GetEnvironmentVariable("no_proxy")));

        http = new HttpClient(new HttpClientHandler { UseProxy = false })
        {
            Timeout = TimeSpan.FromSeconds(5)
        };

        var menu = new ContextMenuStrip();
        menu.Items.Add(new ToolStripMenuItem("QSDM Home Server") { Enabled = false });
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add(validatorItem);
        menu.Items.Add(networkItem);
        menu.Items.Add(minerItem);
        menu.Items.Add(gatewayItem);
        menu.Items.Add(attesterItem);
        menu.Items.Add(treasuryItem);
        menu.Items.Add(watchdogItem);
        menu.Items.Add(guiItem);
        menu.Items.Add(exposureItem);
        menu.Items.Add(lastCheckedItem);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("Open Local GUI", null, (_, _) => OpenLocalGui());
        menu.Items.Add("Open Admin GUI", null, (_, _) => OpenAdminGui());
        menu.Items.Add("Open Diagnostics Folder", null, (_, _) => OpenPath(appDataDir));
        menu.Items.Add("Refresh Now", null, async (_, _) => await CheckAsync(showBalloon: true));
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add(new ToolStripMenuItem("Close is disabled; use Task Manager only for emergency stop.") { Enabled = false });

        currentIcon = QIcon.Create(QIconState.Unknown);
        tray = new NotifyIcon
        {
            Icon = currentIcon,
            Text = "QSDM: checking",
            ContextMenuStrip = menu,
            Visible = true
        };
        tray.DoubleClick += (_, _) => OpenLocalGui();

        timer = new System.Windows.Forms.Timer { Interval = (int)PollInterval.TotalMilliseconds };
        timer.Tick += async (_, _) => await CheckAsync(showBalloon: false);
        timer.Start();

        Log($"started root={qsdmRoot} repo_sha={repositorySha}");
        _ = CheckAsync(showBalloon: true);
    }

    protected override void Dispose(bool disposing)
    {
        if (disposing)
        {
            timer.Dispose();
            tray.Visible = false;
            tray.Dispose();
            currentIcon?.Dispose();
            http.Dispose();
        }
        base.Dispose(disposing);
    }

    private async Task CheckAsync(bool showBalloon)
    {
        if (checking)
        {
            return;
        }
        checking = true;
        try
        {
            var status = await SnapshotAsync();
            ApplyStatus(status, showBalloon);
            WriteStatus(status);
        }
        catch (Exception ex)
        {
            Log("check failed: " + ex.Message);
            var status = StatusSnapshot.FromError(ex.Message);
            ApplyStatus(status, showBalloon);
            WriteStatus(status);
        }
        finally
        {
            checking = false;
        }
    }

    private async Task<StatusSnapshot> SnapshotAsync()
    {
        var checkedAt = DateTime.Now;
        var validatorProcesses = FindProcesses(names: ValidatorProcessNames, prefixes: ValidatorProcessPrefixes);
        var gatewayProcesses = FindProcesses(names: GatewayProcessNames);
        var minerProcesses = FindProcesses(names: ["qsdmminer", "qsdmminer-console"]);
        var guiProcesses = FindProcesses(prefixes: ["qsdm-local-gui"]);
        var attesterProcesses = FindProcesses(names: ["qsdm-attester"]);
        var treasuryProcesses = FindProcesses(names: ["qsdm-treasury-signer", "qsdm-game-signer"]);
        var expectedMode = ReadConfiguredValidatorMode();
        var activeMode = ActiveValidatorMode(validatorProcesses.Select(p => p.Id).ToHashSet());
        var listeners = QueryListeners([4001, 7733, 8080, 8081, 8897, 8898]);

        var validatorTask = ValidatorStatusAsync();
        var guiTask = LocalGuiSnapshotAsync();
        var publicTask = HttpOkAsync("https://api.qsdm.tech/attest/home-validator/api/v1/status");
        var attesterTask = HttpOkAsync("http://127.0.0.1:7733/healthz");
        var referralSignerTask = HttpOkAsync("http://127.0.0.1:8897/healthz");
        var faucetSignerTask = HttpOkAsync("http://127.0.0.1:8898/healthz");
        var minerEnrollmentTask = MinerEnrollmentAsync(ReadMinerNodeId());
        await Task.WhenAll(validatorTask, guiTask, publicTask, attesterTask, referralSignerTask, faucetSignerTask, minerEnrollmentTask);

        var validator = await validatorTask;
        var guiSnapshot = await guiTask;
        var gatewayPublicRaw = guiSnapshot?.GatewayPublic ?? await publicTask;
        var gatewayPublic = StableGatewayPublic(gatewayPublicRaw);
        var chainProgressing = UpdateChainProgress(validator.Height, checkedAt, out var chainRegressed);
        var minerState = QueryServiceState("QSDMMiner");
        var minerActivity = ReadMinerActivity();
        var minerVersion = ReadMinerVersion();
        var minerGitSha = ParseBuildSha(minerVersion);
        var minerBuildStale = repositorySha.Length > 0 && minerGitSha.Length > 0 &&
            !repositorySha.StartsWith(minerGitSha, StringComparison.OrdinalIgnoreCase) &&
            !minerGitSha.StartsWith(repositorySha, StringComparison.OrdinalIgnoreCase);
        var minerEnrollment = await minerEnrollmentTask;
        var exposedListeners = listeners.Where(l => !l.LocalOnly).ToArray();
        var attesterListener = listeners.Where(l => l.Port == 7733).ToArray();
        var modeMismatch = activeMode != "unknown" && !activeMode.Equals(expectedMode, StringComparison.OrdinalIgnoreCase);
        var staleBuild = repositorySha.Length > 0 && validator.GitSha.Length > 0 &&
            !repositorySha.StartsWith(validator.GitSha, StringComparison.OrdinalIgnoreCase) &&
            !validator.GitSha.StartsWith(repositorySha, StringComparison.OrdinalIgnoreCase);

        return new StatusSnapshot
        {
            ValidatorReady = validator.Ready,
            ValidatorProcesses = validatorProcesses.Count,
            ValidatorHeight = validator.Height,
            ValidatorPeers = validator.Peers,
            ValidatorTaskActionsReady = validator.TaskActionsReady,
            ValidatorVersion = validator.Version,
            ValidatorGitSha = validator.GitSha,
            RepositoryGitSha = repositorySha,
            ValidatorBuildStale = staleBuild,
            ValidatorExpectedMode = expectedMode,
            ValidatorActiveMode = activeMode,
            ValidatorModeMismatch = modeMismatch,
            ValidatorChainProgressing = chainProgressing,
            ValidatorChainRegressed = chainRegressed,
            MinerRunning = string.Equals(minerState, "RUNNING", StringComparison.OrdinalIgnoreCase) || minerProcesses.Count > 0,
            MinerProcesses = minerProcesses.Count,
            MinerServiceState = minerState,
            MinerLastActivity = minerActivity.LastActivity,
            MinerLastAcceptedProof = minerActivity.LastAcceptedProof,
            MinerVersion = minerVersion,
            MinerGitSha = minerGitSha,
            MinerBuildStale = minerBuildStale,
            MinerUpdateStaged = File.Exists(minerStagedPath),
            MinerEnrollmentPhase = minerEnrollment.Phase,
            MinerFullyBonded = minerEnrollment.FullyBonded,
            MinerSlashable = minerEnrollment.Slashable,
            MinerStakeDust = minerEnrollment.StakeDust,
            GatewayRunning = gatewayProcesses.Count > 0,
            GatewayPublic = gatewayPublic,
            GatewayProcesses = gatewayProcesses.Count,
            AttesterRunning = attesterProcesses.Count > 0,
            AttesterHealthy = await attesterTask,
            AttesterProcesses = attesterProcesses.Count,
            AttesterLocalOnly = attesterListener.Length > 0 && attesterListener.All(l => l.LocalOnly),
            TreasuryHealthy = await referralSignerTask && await faucetSignerTask,
            TreasuryProcesses = treasuryProcesses.Count,
            ReferralSignerHealthy = await referralSignerTask,
            FaucetSignerHealthy = await faucetSignerTask,
            WatchdogRunning = ProcessFromPidFileIsRunning(Path.Combine(localRoot, "watchdog.pid")),
            GuiRunning = guiProcesses.Count > 0,
            GuiProcesses = guiProcesses.Count,
            ExposureSafe = exposedListeners.Length == 0,
            ExposedListeners = exposedListeners,
            CheckedAt = checkedAt,
            Error = ""
        };
    }

    private void ApplyStatus(StatusSnapshot status, bool showBalloon)
    {
        SetIcon(status.Level);
        validatorItem.Text = status.ValidatorReady
            ? $"Validator: ready h{Dash(status.ValidatorHeight)} ({status.ValidatorProcesses} proc, {ShortSha(status.ValidatorGitSha)})"
            : $"Validator: not ready ({status.ValidatorProcesses} proc)";
        networkItem.Text = $"Network: {status.ValidatorActiveMode}/{status.ValidatorExpectedMode}, {status.ValidatorPeers} peers, {(status.ValidatorChainProgressing ? "progressing" : "stalled")}";
        minerItem.Text = status.MinerRunning
            ? $"Miner: running {ServiceSuffix(status.MinerServiceState)} ({status.MinerProcesses} worker, {status.MinerEnrollmentPhase}, {status.MinerLastAcceptedProof}{(status.MinerUpdateStaged ? ", update staged" : "")})"
            : $"Miner: stopped {ServiceSuffix(status.MinerServiceState)}";
        gatewayItem.Text = status.GatewayRunning
            ? $"Gateway: {(status.GatewayPublic ? "public OK" : "public unavailable")} ({status.GatewayProcesses} proc)"
            : "Gateway: stopped";
        attesterItem.Text = status.AttesterHealthy
            ? $"Attester: healthy ({(status.AttesterLocalOnly ? "loopback" : "EXPOSED")})"
            : $"Attester: unavailable ({status.AttesterProcesses} proc)";
        treasuryItem.Text = status.TreasuryHealthy
            ? $"Treasury signers: healthy ({status.TreasuryProcesses} proc)"
            : $"Treasury signers: referral={Word(status.ReferralSignerHealthy)} faucet={Word(status.FaucetSignerHealthy)}";
        watchdogItem.Text = status.WatchdogRunning ? "Watchdog: running" : "Watchdog: stopped";
        guiItem.Text = status.GuiRunning ? $"GUI: running ({status.GuiProcesses} proc)" : "GUI: stopped";
        exposureItem.Text = status.ExposureSafe
            ? "Exposure: monitored ports are loopback-only"
            : "Exposure: " + string.Join(", ", status.ExposedListeners.Select(l => $"{l.Address}:{l.Port}"));
        lastCheckedItem.Text = $"Last checked: {status.CheckedAt:HH:mm:ss}";

        var title = status.Level switch
        {
            QIconState.Ok => "QSDM Home Server OK",
            QIconState.Warn => "QSDM Home Server needs attention",
            QIconState.Bad => "QSDM Home Server problem",
            _ => "QSDM Home Server checking"
        };
        var message = status.Error.Length > 0 ? status.Error : status.ShortSummary;
        tray.Text = TrimForTray("QSDM: " + message);
        var stateKey = status.StateKey;
        if (showBalloon || (lastStateKey.Length > 0 && stateKey != lastStateKey))
        {
            tray.ShowBalloonTip(5000, title, message, status.Level == QIconState.Bad
                ? ToolTipIcon.Error
                : status.Level == QIconState.Warn ? ToolTipIcon.Warning : ToolTipIcon.Info);
        }
        lastStateKey = stateKey;
    }

    private void SetIcon(QIconState state)
    {
        var next = QIcon.Create(state);
        var old = currentIcon;
        currentIcon = next;
        tray.Icon = next;
        old?.Dispose();
    }

    private async Task<ValidatorApiSnapshot> ValidatorStatusAsync()
    {
        var readyTask = HttpOkAsync("http://127.0.0.1:8080/api/v1/health/ready");
        try
        {
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(4));
            using var resp = await http.GetAsync("http://127.0.0.1:8080/api/v1/status", cts.Token);
            if (!resp.IsSuccessStatusCode)
            {
                return new ValidatorApiSnapshot { Ready = await readyTask };
            }
            using var stream = await resp.Content.ReadAsStreamAsync(cts.Token);
            using var doc = await JsonDocument.ParseAsync(stream, cancellationToken: cts.Token);
            var root = doc.RootElement;
            return new ValidatorApiSnapshot
            {
                Ready = await readyTask,
                Height = LongValue(root, "chain_tip"),
                Peers = (int)(LongValue(root, "peers") ?? 0),
                TaskActionsReady = BoolValue(root, "task_actions_ready"),
                Version = StringValue(root, "version"),
                GitSha = StringValue(root, "git_sha")
            };
        }
        catch
        {
            return new ValidatorApiSnapshot { Ready = await readyTask };
        }
    }

    private async Task<bool> HttpOkAsync(string url)
    {
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(4));
        try
        {
            using var resp = await http.GetAsync(url, cts.Token);
            return resp.IsSuccessStatusCode;
        }
        catch
        {
            return false;
        }
    }

    private async Task<GuiSnapshot?> LocalGuiSnapshotAsync()
    {
        var url = ReadGuiUrl();
        if (!Uri.TryCreate(url, UriKind.Absolute, out var uri) ||
            !uri.Host.Equals("127.0.0.1", StringComparison.OrdinalIgnoreCase))
        {
            return null;
        }
        var snapshotUri = new UriBuilder(uri.Scheme, uri.Host, uri.Port, "/api/snapshot").Uri;
        var token = QueryValue(uri.Query, "t");
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(4));
        try
        {
            using var req = new HttpRequestMessage(HttpMethod.Get, snapshotUri);
            if (!string.IsNullOrWhiteSpace(token))
            {
                req.Headers.TryAddWithoutValidation("X-QSDM-Token", token);
            }
            using var resp = await http.SendAsync(req, cts.Token);
            if (!resp.IsSuccessStatusCode)
            {
                return null;
            }
            using var stream = await resp.Content.ReadAsStreamAsync(cts.Token);
            using var doc = await JsonDocument.ParseAsync(stream, cancellationToken: cts.Token);
            if (doc.RootElement.TryGetProperty("gateway", out var gateway) &&
                gateway.TryGetProperty("public_ok", out var publicOk) &&
                (publicOk.ValueKind == JsonValueKind.True || publicOk.ValueKind == JsonValueKind.False))
            {
                return new GuiSnapshot(publicOk.GetBoolean());
            }
        }
        catch
        {
            // The GUI is an optional secondary public-gateway signal.
        }
        return null;
    }

    private bool StableGatewayPublic(bool current)
    {
        var now = DateTime.Now;
        if (current)
        {
            gatewayPublicFailures = 0;
            lastGatewayPublicOk = now;
            return true;
        }
        gatewayPublicFailures++;
        if (!lastGatewayPublicOk.HasValue)
        {
            return false;
        }
        return gatewayPublicFailures < 3 && now - lastGatewayPublicOk.Value < TimeSpan.FromMinutes(2);
    }

    private bool UpdateChainProgress(long? height, DateTime now, out bool regressed)
    {
        regressed = false;
        if (!height.HasValue)
        {
            return false;
        }
        if (!lastValidatorHeight.HasValue)
        {
            lastValidatorHeight = height;
            lastHeightProgressAt = now;
            return true;
        }
        if (height > lastValidatorHeight)
        {
            lastValidatorHeight = height;
            lastHeightProgressAt = now;
            return true;
        }
        if (height < lastValidatorHeight)
        {
            regressed = true;
            return false;
        }
        return now - lastHeightProgressAt <= ChainStallThreshold;
    }

    private string ReadConfiguredValidatorMode()
    {
        try
        {
            using var doc = JsonDocument.Parse(File.ReadAllText(Path.Combine(localRoot, "validator-mode.json")));
            if (StringValue(doc.RootElement, "mode").Equals("networked", StringComparison.OrdinalIgnoreCase))
            {
                return "networked";
            }
        }
        catch
        {
            // Missing config means the local solo mode.
        }
        return "solo";
    }

    private string ActiveValidatorMode(HashSet<int> validatorPids)
    {
        foreach (var candidate in new[] { (Mode: "networked", Dir: "run-networked"), (Mode: "solo", Dir: "run-v2") })
        {
            var pid = ReadPid(Path.Combine(localRoot, candidate.Dir, "qsdm.autostart.pid"));
            if (pid.HasValue && validatorPids.Contains(pid.Value))
            {
                return candidate.Mode;
            }
        }
        return "unknown";
    }

    private MinerActivity ReadMinerActivity()
    {
        var path = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".qsdm", "miner.log");
        try
        {
            using var file = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
            var bytesToRead = (int)Math.Min(file.Length, 512 * 1024);
            file.Seek(-bytesToRead, SeekOrigin.End);
            var buffer = new byte[bytesToRead];
            _ = file.Read(buffer, 0, buffer.Length);
            var lines = Encoding.UTF8.GetString(buffer).Split('\n', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
            var accepted = lines.LastOrDefault(line => line.Contains("[PASS] proof ACCEPTED", StringComparison.OrdinalIgnoreCase));
            return new MinerActivity(File.GetLastWriteTime(path), accepted == null ? "no recent proof" : accepted.Split(' ', 2)[0]);
        }
        catch
        {
            return new MinerActivity(null, "proof unknown");
        }
    }

    private string ReadMinerNodeId()
    {
        try
        {
            foreach (var line in File.ReadLines(minerConfigPath))
            {
                var trimmed = line.Trim();
                if (!trimmed.StartsWith("node_id", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                var split = trimmed.IndexOf('=');
                if (split >= 0)
                {
                    return trimmed[(split + 1)..].Trim().Trim('"', '\'');
                }
            }
        }
        catch
        {
            // Missing miner config is reflected by an unknown enrollment.
        }
        return "";
    }

    private async Task<MinerEnrollmentSnapshot> MinerEnrollmentAsync(string nodeId)
    {
        if (string.IsNullOrWhiteSpace(nodeId))
        {
            return new MinerEnrollmentSnapshot();
        }
        try
        {
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(4));
            var url = "http://127.0.0.1:8080/api/v1/mining/enrollment/" + Uri.EscapeDataString(nodeId);
            using var resp = await http.GetAsync(url, cts.Token);
            if (!resp.IsSuccessStatusCode)
            {
                return new MinerEnrollmentSnapshot { Phase = resp.StatusCode == System.Net.HttpStatusCode.NotFound ? "not_found" : "unknown" };
            }
            using var stream = await resp.Content.ReadAsStreamAsync(cts.Token);
            using var doc = await JsonDocument.ParseAsync(stream, cancellationToken: cts.Token);
            var root = doc.RootElement;
            return new MinerEnrollmentSnapshot
            {
                Phase = StringValue(root, "phase"),
                FullyBonded = BoolValue(root, "fully_bonded"),
                Slashable = BoolValue(root, "slashable"),
                StakeDust = LongValue(root, "stake_dust") ?? 0
            };
        }
        catch
        {
            return new MinerEnrollmentSnapshot();
        }
    }

    private static string ReadBinaryVersion(string path)
    {
        if (!File.Exists(path))
        {
            return "";
        }
        try
        {
            using var process = new Process();
            process.StartInfo = new ProcessStartInfo
            {
                FileName = path,
                ArgumentList = { "--version" },
                UseShellExecute = false,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true
            };
            process.Start();
            var output = (process.StandardOutput.ReadToEnd() + process.StandardError.ReadToEnd()).Trim();
            if (!process.WaitForExit(3000))
            {
                process.Kill();
                return "";
            }
            return output;
        }
        catch
        {
            return "";
        }
    }

    private string ReadMinerVersion()
    {
        try
        {
            var writeUtc = File.GetLastWriteTimeUtc(minerRuntimePath);
            if (minerVersionCache.Length > 0 && writeUtc == minerVersionWriteUtc)
            {
                return minerVersionCache;
            }
            minerVersionCache = ReadBinaryVersion(minerRuntimePath);
            minerVersionWriteUtc = writeUtc;
            return minerVersionCache;
        }
        catch
        {
            return minerVersionCache;
        }
    }

    private static string ParseBuildSha(string version)
    {
        var open = version.IndexOf('(');
        var comma = open >= 0 ? version.IndexOf(',', open + 1) : -1;
        return open >= 0 && comma > open ? version[(open + 1)..comma].Trim() : "";
    }

    private static List<ProcessInfo> FindProcesses(string[]? names = null, string[]? prefixes = null)
    {
        var exact = new HashSet<string>(names ?? [], StringComparer.OrdinalIgnoreCase);
        var starts = prefixes ?? [];
        var found = new List<ProcessInfo>();
        foreach (var process in Process.GetProcesses())
        {
            try
            {
                var name = process.ProcessName;
                if (exact.Contains(name) || starts.Any(prefix => name.StartsWith(prefix, StringComparison.OrdinalIgnoreCase)))
                {
                    found.Add(new ProcessInfo(process.Id, name));
                }
            }
            catch
            {
                // Process may exit or deny metadata while enumerating.
            }
            finally
            {
                process.Dispose();
            }
        }
        return found.GroupBy(p => p.Id).Select(g => g.First()).ToList();
    }

    private static List<ListenerInfo> QueryListeners(int[] ports)
    {
        var wanted = ports.ToHashSet();
        var listeners = new List<ListenerInfo>();
        try
        {
            using var process = new Process();
            process.StartInfo = new ProcessStartInfo
            {
                FileName = "netstat.exe",
                ArgumentList = { "-ano", "-p", "tcp" },
                UseShellExecute = false,
                RedirectStandardOutput = true,
                CreateNoWindow = true
            };
            process.Start();
            var output = process.StandardOutput.ReadToEnd();
            if (!process.WaitForExit(3000))
            {
                process.Kill();
                return listeners;
            }
            foreach (var line in output.Split('\n'))
            {
                var fields = line.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries);
                if (fields.Length < 5 || !fields[0].Equals("TCP", StringComparison.OrdinalIgnoreCase) ||
                    !fields[3].Equals("LISTENING", StringComparison.OrdinalIgnoreCase))
                {
                    continue;
                }
                var endpoint = fields[1];
                var split = endpoint.LastIndexOf(':');
                if (split < 0 || !int.TryParse(endpoint[(split + 1)..], out var port) || !wanted.Contains(port))
                {
                    continue;
                }
                var address = endpoint[..split].Trim('[', ']');
                _ = int.TryParse(fields[4], out var pid);
                listeners.Add(new ListenerInfo(address, port, pid, address is "127.0.0.1" or "::1"));
            }
        }
        catch
        {
            // Exposure will be reported unknown rather than crashing monitoring.
        }
        return listeners;
    }

    private static string QueryServiceState(string serviceName)
    {
        try
        {
            using var p = new Process();
            p.StartInfo = new ProcessStartInfo
            {
                FileName = "sc.exe",
                ArgumentList = { "query", serviceName },
                UseShellExecute = false,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true
            };
            p.Start();
            var output = p.StandardOutput.ReadToEnd() + p.StandardError.ReadToEnd();
            if (!p.WaitForExit(3000))
            {
                p.Kill();
                return "UNKNOWN";
            }
            foreach (var line in output.Split('\n'))
            {
                var trimmed = line.Trim();
                if (trimmed.StartsWith("STATE", StringComparison.OrdinalIgnoreCase))
                {
                    var parts = trimmed.Split(' ', StringSplitOptions.RemoveEmptyEntries);
                    return parts.Length >= 4 ? parts[3] : "UNKNOWN";
                }
            }
        }
        catch
        {
            // Service metadata is optional when running an interactive miner.
        }
        return "UNKNOWN";
    }

    private static bool ProcessFromPidFileIsRunning(string path)
    {
        var pid = ReadPid(path);
        if (!pid.HasValue)
        {
            return false;
        }
        try
        {
            using var process = Process.GetProcessById(pid.Value);
            return !process.HasExited;
        }
        catch
        {
            return false;
        }
    }

    private static int? ReadPid(string path)
    {
        try
        {
            return int.TryParse(File.ReadAllText(path).Trim(), out var pid) ? pid : null;
        }
        catch
        {
            return null;
        }
    }

    private void OpenLocalGui() => OpenUrl(ReadGuiUrl());

    private void OpenAdminGui()
    {
        if (File.Exists(adminGuiLauncher))
        {
            Process.Start(new ProcessStartInfo
            {
                FileName = adminGuiLauncher,
                UseShellExecute = true,
                WorkingDirectory = Path.GetDirectoryName(adminGuiLauncher) ?? qsdmRoot
            });
            return;
        }
        OpenLocalGui();
    }

    private string ReadGuiUrl()
    {
        try
        {
            if (File.Exists(guiUrlFile))
            {
                var url = File.ReadAllText(guiUrlFile).Trim();
                if (url.StartsWith("http://127.0.0.1:", StringComparison.OrdinalIgnoreCase))
                {
                    return url;
                }
            }
        }
        catch
        {
            // Fall through to dashboard.
        }
        return "http://127.0.0.1:8081/";
    }

    private static void OpenPath(string path)
    {
        try
        {
            Process.Start(new ProcessStartInfo { FileName = "explorer.exe", ArgumentList = { path }, UseShellExecute = true });
        }
        catch (Exception ex)
        {
            MessageBox.Show(ex.Message, "QSDM Tray Monitor", MessageBoxButtons.OK, MessageBoxIcon.Warning);
        }
    }

    private static void OpenUrl(string url)
    {
        try
        {
            Process.Start(new ProcessStartInfo { FileName = url, UseShellExecute = true });
        }
        catch (Exception ex)
        {
            MessageBox.Show(ex.Message, "QSDM Tray Monitor", MessageBoxButtons.OK, MessageBoxIcon.Warning);
        }
    }

    private static string FindQsdmRoot(string[] args)
    {
        var explicitRoot = ArgValue(args, "--root") ?? Environment.GetEnvironmentVariable("QSDM_ROOT");
        if (!string.IsNullOrWhiteSpace(explicitRoot) && Directory.Exists(explicitRoot))
        {
            return Path.GetFullPath(explicitRoot);
        }
        var starts = new List<string>();
        if (!string.IsNullOrWhiteSpace(AppContext.BaseDirectory))
        {
            starts.Add(AppContext.BaseDirectory);
        }
        starts.Add(Environment.CurrentDirectory);
        foreach (var start in starts)
        {
            var dir = new DirectoryInfo(Path.GetFullPath(start));
            for (var i = 0; dir != null && i < 10; i++, dir = dir.Parent)
            {
                if (File.Exists(Path.Combine(dir.FullName, "qsdm.yaml")))
                {
                    return dir.FullName;
                }
                if (File.Exists(Path.Combine(dir.FullName, "QSDM", "qsdm.yaml")))
                {
                    return Path.Combine(dir.FullName, "QSDM");
                }
            }
        }
        return Path.Combine(Environment.CurrentDirectory, "QSDM");
    }

    private static string? ArgValue(string[] args, string name)
    {
        for (var i = 0; i < args.Length; i++)
        {
            if (args[i].Equals(name, StringComparison.OrdinalIgnoreCase) && i + 1 < args.Length)
            {
                return args[i + 1];
            }
            if (args[i].StartsWith(name + "=", StringComparison.OrdinalIgnoreCase))
            {
                return args[i][(name.Length + 1)..];
            }
        }
        return null;
    }

    private static string ReadRepositorySha(string root)
    {
        try
        {
            using var process = new Process();
            process.StartInfo = new ProcessStartInfo
            {
                FileName = "git.exe",
                ArgumentList = { "-C", root, "rev-parse", "--short", "HEAD" },
                UseShellExecute = false,
                RedirectStandardOutput = true,
                CreateNoWindow = true
            };
            process.Start();
            var output = process.StandardOutput.ReadToEnd().Trim();
            return process.WaitForExit(3000) && process.ExitCode == 0 ? output : "";
        }
        catch
        {
            return "";
        }
    }

    private static string QueryValue(string query, string name)
    {
        if (query.StartsWith('?'))
        {
            query = query[1..];
        }
        foreach (var part in query.Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            var pieces = part.Split('=', 2);
            if (Uri.UnescapeDataString(pieces[0]).Equals(name, StringComparison.OrdinalIgnoreCase))
            {
                return pieces.Length == 2 ? Uri.UnescapeDataString(pieces[1]) : "";
            }
        }
        return "";
    }

    private void WriteStatus(StatusSnapshot status)
    {
        var tempPath = statusPath + ".tmp";
        try
        {
            var json = JsonSerializer.Serialize(status, new JsonSerializerOptions
            {
                WriteIndented = true,
                DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
            });
            File.WriteAllText(tempPath, json);
            File.Move(tempPath, statusPath, true);
        }
        catch (Exception ex)
        {
            try { File.Delete(tempPath); } catch { }
            Log("status write failed: " + ex.Message);
        }
    }

    private void Log(string message)
    {
        try
        {
            Directory.CreateDirectory(appDataDir);
            File.AppendAllText(logPath, $"[{DateTime.Now:yyyy-MM-dd HH:mm:ss}] {message}{Environment.NewLine}");
        }
        catch
        {
            // Tray monitoring must keep running even if diagnostics cannot be written.
        }
    }

    private static long? LongValue(JsonElement root, string name) =>
        root.TryGetProperty(name, out var value) && value.TryGetInt64(out var result) ? result : null;
    private static string StringValue(JsonElement root, string name) =>
        root.TryGetProperty(name, out var value) && value.ValueKind == JsonValueKind.String ? value.GetString() ?? "" : "";
    private static bool BoolValue(JsonElement root, string name) =>
        root.TryGetProperty(name, out var value) && value.ValueKind == JsonValueKind.True;
    private static string MergeNoProxy(string? current)
    {
        var required = new[] { "127.0.0.1", "localhost", "api.qsdm.tech" };
        var parts = (current ?? "").Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).ToList();
        foreach (var item in required)
        {
            if (!parts.Any(p => p.Equals(item, StringComparison.OrdinalIgnoreCase)))
            {
                parts.Add(item);
            }
        }
        return string.Join(",", parts);
    }
    private static string Dash(long? value) => value?.ToString(CultureInfo.InvariantCulture) ?? "-";
    private static string ShortSha(string value) => value.Length > 8 ? value[..8] : value.Length == 0 ? "build ?" : value;
    private static string ServiceSuffix(string state) => string.IsNullOrWhiteSpace(state) || state == "UNKNOWN" ? "" : $"service {state}";
    private static string Word(bool value) => value ? "OK" : "DOWN";
    private static string TrimForTray(string text) => text.Length <= 63 ? text : text[..60] + "...";
}

internal sealed class ValidatorApiSnapshot
{
    public bool Ready { get; init; }
    public long? Height { get; init; }
    public int Peers { get; init; }
    public bool TaskActionsReady { get; init; }
    public string Version { get; init; } = "";
    public string GitSha { get; init; } = "";
}

internal sealed record GuiSnapshot(bool GatewayPublic);
internal sealed record ProcessInfo(int Id, string Name);
internal sealed record ListenerInfo(string Address, int Port, int Pid, bool LocalOnly);
internal sealed record MinerActivity(DateTime? LastActivity, string LastAcceptedProof);

internal sealed class MinerEnrollmentSnapshot
{
    public string Phase { get; init; } = "unknown";
    public bool FullyBonded { get; init; }
    public bool Slashable { get; init; }
    public long StakeDust { get; init; }
}

internal sealed class StatusSnapshot
{
    public bool ValidatorReady { get; init; }
    public int ValidatorProcesses { get; init; }
    public long? ValidatorHeight { get; init; }
    public int ValidatorPeers { get; init; }
    public bool ValidatorTaskActionsReady { get; init; }
    public string ValidatorVersion { get; init; } = "";
    public string ValidatorGitSha { get; init; } = "";
    public string RepositoryGitSha { get; init; } = "";
    public bool ValidatorBuildStale { get; init; }
    public string ValidatorExpectedMode { get; init; } = "unknown";
    public string ValidatorActiveMode { get; init; } = "unknown";
    public bool ValidatorModeMismatch { get; init; }
    public bool ValidatorChainProgressing { get; init; }
    public bool ValidatorChainRegressed { get; init; }
    public bool MinerRunning { get; init; }
    public int MinerProcesses { get; init; }
    public string MinerServiceState { get; init; } = "UNKNOWN";
    public DateTime? MinerLastActivity { get; init; }
    public string MinerLastAcceptedProof { get; init; } = "proof unknown";
    public string MinerVersion { get; init; } = "";
    public string MinerGitSha { get; init; } = "";
    public bool MinerBuildStale { get; init; }
    public bool MinerUpdateStaged { get; init; }
    public string MinerEnrollmentPhase { get; init; } = "unknown";
    public bool MinerFullyBonded { get; init; }
    public bool MinerSlashable { get; init; }
    public long MinerStakeDust { get; init; }
    public bool GatewayRunning { get; init; }
    public bool GatewayPublic { get; init; }
    public int GatewayProcesses { get; init; }
    public bool AttesterRunning { get; init; }
    public bool AttesterHealthy { get; init; }
    public int AttesterProcesses { get; init; }
    public bool AttesterLocalOnly { get; init; }
    public bool TreasuryHealthy { get; init; }
    public int TreasuryProcesses { get; init; }
    public bool ReferralSignerHealthy { get; init; }
    public bool FaucetSignerHealthy { get; init; }
    public bool WatchdogRunning { get; init; }
    public bool GuiRunning { get; init; }
    public int GuiProcesses { get; init; }
    public bool ExposureSafe { get; init; }
    public ListenerInfo[] ExposedListeners { get; init; } = [];
    public DateTime CheckedAt { get; init; }
    public string Error { get; init; } = "";

    public static StatusSnapshot FromError(string message) => new() { CheckedAt = DateTime.Now, Error = message };

    [JsonIgnore]
    public QIconState Level
    {
        get
        {
            if (Error.Length > 0 || !ValidatorReady || ValidatorModeMismatch || ValidatorChainRegressed ||
                !ValidatorTaskActionsReady || !WatchdogRunning || !ExposureSafe)
            {
                return QIconState.Bad;
            }
            if (ValidatorProcesses != 1 || !ValidatorChainProgressing || ValidatorBuildStale ||
                (ValidatorExpectedMode == "networked" && ValidatorPeers == 0) || !MinerRunning || MinerProcesses > 1 ||
                MinerBuildStale || (MinerEnrollmentPhase.Length > 0 && MinerEnrollmentPhase != "unknown" && MinerEnrollmentPhase != "active") ||
                !GatewayRunning || !GatewayPublic || GatewayProcesses != 1 || !AttesterRunning || !AttesterHealthy ||
                !AttesterLocalOnly || !TreasuryHealthy || TreasuryProcesses != 2 || !GuiRunning || GuiProcesses != 1)
            {
                return QIconState.Warn;
            }
            return QIconState.Ok;
        }
    }

    [JsonIgnore]
    public string ShortSummary
    {
        get
        {
            if (Error.Length > 0) return Error;
            if (!ValidatorReady) return "validator not ready";
            if (ValidatorModeMismatch) return $"validator mode mismatch: {ValidatorActiveMode}, expected {ValidatorExpectedMode}";
            if (ValidatorChainRegressed) return "validator chain height regressed";
            if (!ValidatorTaskActionsReady) return "validator task actions are not ready";
            if (!WatchdogRunning) return "stack watchdog stopped";
            if (!ExposureSafe) return "a monitored service is exposed outside loopback";
            if (ValidatorProcesses != 1) return $"validator process count is {ValidatorProcesses}";
            if (!ValidatorChainProgressing) return "validator chain is not progressing";
            if (ValidatorBuildStale) return $"validator build {ValidatorGitSha} is older than source {RepositoryGitSha}";
            if (ValidatorExpectedMode == "networked" && ValidatorPeers == 0) return "networked validator has zero peers";
            if (!MinerRunning) return "miner stopped";
            if (MinerProcesses > 1) return $"duplicate miner workers: {MinerProcesses}";
            if (MinerBuildStale) return MinerUpdateStaged
                ? $"miner update {RepositoryGitSha} is staged; restart Windows or the service to apply"
                : $"miner build {MinerGitSha} is older than source {RepositoryGitSha}";
            if (MinerEnrollmentPhase.Length > 0 && MinerEnrollmentPhase != "unknown" && MinerEnrollmentPhase != "active")
                return $"miner enrollment is {MinerEnrollmentPhase}";
            if (!GatewayRunning) return "home gateway stopped";
            if (!GatewayPublic) return "public home gateway unavailable";
            if (GatewayProcesses != 1) return $"gateway process count is {GatewayProcesses}";
            if (!AttesterHealthy) return "attester unavailable";
            if (!AttesterLocalOnly) return "attester is exposed outside loopback";
            if (!TreasuryHealthy) return "one or more treasury signers are unavailable";
            if (TreasuryProcesses != 2) return $"treasury signer process count is {TreasuryProcesses}";
            if (!GuiRunning) return "local GUI stopped";
            if (GuiProcesses != 1) return $"local GUI process count is {GuiProcesses}";
            return $"OK h{ValidatorHeight?.ToString(CultureInfo.InvariantCulture) ?? "-"} peers {ValidatorPeers}";
        }
    }

    [JsonIgnore]
    public string StateKey => string.Join('|', Level, ValidatorReady, ValidatorActiveMode, ValidatorExpectedMode,
        ValidatorPeers, ValidatorChainProgressing, ValidatorBuildStale, MinerRunning, MinerProcesses,
        MinerBuildStale, MinerUpdateStaged, MinerEnrollmentPhase,
        GatewayRunning, GatewayPublic, GatewayProcesses, AttesterHealthy, AttesterLocalOnly,
        TreasuryHealthy, WatchdogRunning, GuiRunning, ExposureSafe, Error);
}

internal enum QIconState
{
    Unknown,
    Ok,
    Warn,
    Bad
}

internal static class QIcon
{
    public static Icon Create(QIconState state)
    {
        var badge = state switch
        {
            QIconState.Ok => Color.FromArgb(31, 122, 83),
            QIconState.Warn => Color.FromArgb(154, 101, 0),
            QIconState.Bad => Color.FromArgb(180, 35, 24),
            _ => Color.FromArgb(98, 105, 117)
        };
        using var bmp = new Bitmap(64, 64);
        using (var g = Graphics.FromImage(bmp))
        {
            g.Clear(Color.Transparent);
            g.SmoothingMode = System.Drawing.Drawing2D.SmoothingMode.AntiAlias;
            using var bg = new SolidBrush(Color.FromArgb(23, 26, 31));
            using var fg = new SolidBrush(Color.FromArgb(84, 209, 143));
            using var badgeBrush = new SolidBrush(badge);
            g.FillRoundedRectangle(bg, new Rectangle(4, 4, 56, 56), 12);
            using var font = new Font("Segoe UI", 31, FontStyle.Bold, GraphicsUnit.Pixel);
            var textSize = g.MeasureString("Q", font);
            g.DrawString("Q", font, fg, (64 - textSize.Width) / 2 + 1, (64 - textSize.Height) / 2 - 2);
            g.FillEllipse(badgeBrush, 43, 43, 17, 17);
            using var ring = new Pen(Color.White, 3);
            g.DrawEllipse(ring, 43, 43, 17, 17);
        }
        var handle = bmp.GetHicon();
        try
        {
            return (Icon)Icon.FromHandle(handle).Clone();
        }
        finally
        {
            _ = DestroyIcon(handle);
        }
    }

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool DestroyIcon(IntPtr hIcon);
}

internal static class GraphicsExtensions
{
    public static void FillRoundedRectangle(this Graphics graphics, Brush brush, Rectangle bounds, int radius)
    {
        using var path = new System.Drawing.Drawing2D.GraphicsPath();
        var diameter = radius * 2;
        path.AddArc(bounds.Left, bounds.Top, diameter, diameter, 180, 90);
        path.AddArc(bounds.Right - diameter, bounds.Top, diameter, diameter, 270, 90);
        path.AddArc(bounds.Right - diameter, bounds.Bottom - diameter, diameter, diameter, 0, 90);
        path.AddArc(bounds.Left, bounds.Bottom - diameter, diameter, diameter, 90, 90);
        path.CloseFigure();
        graphics.FillPath(brush, path);
    }
}
