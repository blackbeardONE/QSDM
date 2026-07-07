using System.Diagnostics;
using System.Drawing;
using System.Net.Http;
using System.Runtime.InteropServices;
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

    private readonly NotifyIcon tray;
    private readonly System.Windows.Forms.Timer timer;
    private readonly HttpClient http;
    private readonly string qsdmRoot;
    private readonly string guiUrlFile;
    private readonly string adminGuiLauncher;
    private readonly string appDataDir;
    private readonly string statusPath;
    private readonly string logPath;
    private readonly ToolStripMenuItem validatorItem = new("Validator: checking");
    private readonly ToolStripMenuItem minerItem = new("Miner: checking");
    private readonly ToolStripMenuItem gatewayItem = new("Gateway: checking");
    private readonly ToolStripMenuItem guiItem = new("GUI: checking");
    private readonly ToolStripMenuItem exposureItem = new("Exposure: checking");
    private readonly ToolStripMenuItem lastCheckedItem = new("Last checked: -");
    private string lastStateKey = "";
    private DateTime? lastGatewayPublicOk;
    private int gatewayPublicFailures;
    private Icon? currentIcon;
    private bool checking;

    public MonitorContext(string[] args)
    {
        qsdmRoot = FindQsdmRoot(args);
        guiUrlFile = Path.Combine(qsdmRoot, "source", ".cache", "local-validator", "local-gui-persist.url");
        adminGuiLauncher = Path.Combine(qsdmRoot, "scripts", "QSDM Admin GUI.cmd");
        appDataDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData), "QSDM-Tray-Monitor");
        statusPath = Path.Combine(appDataDir, "status.json");
        logPath = Path.Combine(appDataDir, "monitor.log");

        Directory.CreateDirectory(appDataDir);
        Environment.SetEnvironmentVariable("NO_PROXY", MergeNoProxy(Environment.GetEnvironmentVariable("NO_PROXY")));
        Environment.SetEnvironmentVariable("no_proxy", MergeNoProxy(Environment.GetEnvironmentVariable("no_proxy")));

        http = new HttpClient(new HttpClientHandler { UseProxy = false })
        {
            Timeout = TimeSpan.FromSeconds(4)
        };

        var menu = new ContextMenuStrip();
        menu.Items.Add(new ToolStripMenuItem("QSDM Local Monitor") { Enabled = false });
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add(validatorItem);
        menu.Items.Add(minerItem);
        menu.Items.Add(gatewayItem);
        menu.Items.Add(guiItem);
        menu.Items.Add(exposureItem);
        menu.Items.Add(lastCheckedItem);
        menu.Items.Add(new ToolStripSeparator());
        menu.Items.Add("Open Local GUI", null, (_, _) => OpenLocalGui());
        menu.Items.Add("Open Admin GUI", null, (_, _) => OpenAdminGui());
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

        Log($"started root={qsdmRoot}");
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
        var validatorProc = CountProcesses(
            "qsdm-local-validator-sqlite.hotfix",
            "qsdm-local-validator-sqlite.candidate",
            "qsdm-local-validator-sqlite.new",
            "qsdm-local-validator-sqlite",
            "qsdm-local-validator-hive.new",
            "qsdm-local-validator-hive",
            "qsdm-local-validator-next",
            "qsdm-local-validator",
            "qsdm-sqlite-next",
            "qsdm-sqlite",
            "qsdm-new",
            "qsdm");
        var gatewayProc = CountProcesses("qsdm-home-gateway-hive.new", "qsdm-home-gateway-hive", "qsdm-home-gateway");
        var minerProc = CountProcesses("qsdmminer", "qsdmminer-console");
        var guiProc = CountProcesses("qsdm-local-gui-hive-v2", "qsdm-local-gui-hive", "qsdm-local-gui-persist", "qsdm-local-gui-next", "qsdm-local-gui-sqlite", "qsdm-local-gui");

        var validatorReady = await HttpOkAsync("http://127.0.0.1:8080/api/v1/health/ready");
        var validatorHeight = await ValidatorHeightAsync();
        var guiSnapshot = await LocalGuiSnapshotAsync();
        if (guiSnapshot?.ValidatorHeight is long guiHeight)
        {
            validatorHeight = guiHeight;
        }
        var gatewayPublic = guiSnapshot?.GatewayPublic
            ?? await HttpOkAsync("https://api.qsdm.tech/attest/home-validator/api/v1/status");
        gatewayPublic = StableGatewayPublic(gatewayPublic);
        var minerState = QueryMinerServiceState();

        return new StatusSnapshot(
            ValidatorReady: validatorReady,
            ValidatorProcesses: validatorProc,
            ValidatorHeight: validatorHeight,
            MinerRunning: string.Equals(minerState, "RUNNING", StringComparison.OrdinalIgnoreCase) || minerProc > 0,
            MinerProcesses: minerProc,
            MinerServiceState: minerState,
            GatewayRunning: gatewayProc > 0,
            GatewayPublic: gatewayPublic,
            GatewayProcesses: gatewayProc,
            GuiRunning: guiProc > 0,
            GuiProcesses: guiProc,
            CheckedAt: DateTime.Now,
            Error: "");
    }

    private void ApplyStatus(StatusSnapshot status, bool showBalloon)
    {
        var state = status.Level;
        SetIcon(state);

        validatorItem.Text = status.ValidatorReady
            ? $"Validator: ready height {Dash(status.ValidatorHeight)} ({status.ValidatorProcesses} proc)"
            : $"Validator: not ready ({status.ValidatorProcesses} proc)";
        minerItem.Text = status.MinerRunning
            ? $"Miner: running {ServiceSuffix(status.MinerServiceState)} ({status.MinerProcesses} worker)"
            : $"Miner: stopped {ServiceSuffix(status.MinerServiceState)}";
        gatewayItem.Text = status.GatewayRunning
            ? $"Gateway: {(status.GatewayPublic ? "public OK" : "local only")} ({status.GatewayProcesses} proc)"
            : "Gateway: stopped";
        guiItem.Text = status.GuiRunning ? $"GUI: running ({status.GuiProcesses} proc)" : "GUI: stopped";
        exposureItem.Text = "Exposure: validator/API/dashboard localhost-only";
        lastCheckedItem.Text = $"Last checked: {status.CheckedAt:HH:mm:ss}";

        var title = status.Level switch
        {
            QIconState.Ok => "QSDM OK",
            QIconState.Warn => "QSDM needs attention",
            QIconState.Bad => "QSDM problem",
            _ => "QSDM checking"
        };
        var message = status.Error.Length > 0 ? status.Error : status.ShortSummary;
        tray.Text = TrimForTray($"QSDM: {message}");

        var stateKey = status.StateKey;
        if (showBalloon || (lastStateKey.Length > 0 && stateKey != lastStateKey))
        {
            tray.ShowBalloonTip(4000, title, message, status.Level == QIconState.Bad
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

    private async Task<bool> HttpOkAsync(string url)
    {
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(4));
        try
        {
            using var resp = await http.GetAsync(url, cts.Token);
            return (int)resp.StatusCode >= 200 && (int)resp.StatusCode < 300;
        }
        catch
        {
            return false;
        }
    }

    private async Task<long?> ValidatorHeightAsync()
    {
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(4));
        try
        {
            using var stream = await http.GetStreamAsync("http://127.0.0.1:8080/api/v1/status", cts.Token);
            using var doc = await JsonDocument.ParseAsync(stream, cancellationToken: cts.Token);
            if (doc.RootElement.TryGetProperty("consensus", out var consensus) &&
                consensus.TryGetProperty("height", out var height) &&
                height.TryGetInt64(out var value))
            {
                return value;
            }
            if (doc.RootElement.TryGetProperty("chain_tip", out var chainTip) &&
                chainTip.TryGetInt64(out var tipValue))
            {
                return tipValue;
            }
            if (doc.RootElement.TryGetProperty("height", out var topHeight) &&
                topHeight.TryGetInt64(out var heightValue))
            {
                return heightValue;
            }
        }
        catch
        {
            // Height is optional; health is the main signal.
        }
        return null;
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
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(5));
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

            bool? gatewayPublic = null;
            long? validatorHeight = null;
            if (doc.RootElement.TryGetProperty("gateway", out var gateway))
            {
                if (gateway.TryGetProperty("public_ok", out var publicOk) &&
                    (publicOk.ValueKind == JsonValueKind.True || publicOk.ValueKind == JsonValueKind.False))
                {
                    gatewayPublic = publicOk.GetBoolean();
                }
                if (gateway.TryGetProperty("chain_tip", out var gatewayTip) &&
                    gatewayTip.TryGetInt64(out var gatewayHeight))
                {
                    validatorHeight = gatewayHeight;
                }
            }
            if (doc.RootElement.TryGetProperty("validator", out var validator) &&
                validator.TryGetProperty("chain_tip", out var validatorTip) &&
                validatorTip.TryGetInt64(out var height))
            {
                validatorHeight = height;
            }
            return new GuiSnapshot(gatewayPublic, validatorHeight);
        }
        catch
        {
            return null;
        }
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
        if (gatewayPublicFailures < 3)
        {
            return true;
        }
        if (lastGatewayPublicOk.HasValue && now - lastGatewayPublicOk.Value < TimeSpan.FromMinutes(2))
        {
            return true;
        }
        return false;
    }

    private static int CountProcesses(params string[] names)
    {
        var count = 0;
        foreach (var name in names)
        {
            try
            {
                count += Process.GetProcessesByName(name).Length;
            }
            catch
            {
                // Process enumeration can be partially denied; absence is safer.
            }
        }
        return count;
    }

    private static string QueryMinerServiceState()
    {
        try
        {
            using var p = new Process();
            p.StartInfo = new ProcessStartInfo
            {
                FileName = "sc.exe",
                ArgumentList = { "query", "QSDMMiner" },
                UseShellExecute = false,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                CreateNoWindow = true
            };
            p.Start();
            if (!p.WaitForExit(3000))
            {
                try { p.Kill(); } catch { }
                return "UNKNOWN";
            }
            var output = p.StandardOutput.ReadToEnd() + p.StandardError.ReadToEnd();
            foreach (var line in output.Split('\n'))
            {
                var trimmed = line.Trim();
                if (trimmed.StartsWith("STATE", StringComparison.OrdinalIgnoreCase))
                {
                    var parts = trimmed.Split(' ', StringSplitOptions.RemoveEmptyEntries);
                    if (parts.Length >= 4)
                    {
                        return parts[3];
                    }
                }
            }
        }
        catch
        {
            return "UNKNOWN";
        }
        return "UNKNOWN";
    }

    private void OpenLocalGui()
    {
        var url = ReadGuiUrl();
        OpenUrl(url);
    }

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
                var direct = Path.Combine(dir.FullName, "qsdm.yaml");
                if (File.Exists(direct))
                {
                    return dir.FullName;
                }
                var child = Path.Combine(dir.FullName, "QSDM", "qsdm.yaml");
                if (File.Exists(child))
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

    private static string Dash(long? value) => value.HasValue ? value.Value.ToString() : "-";

    private static string ServiceSuffix(string state) => string.IsNullOrWhiteSpace(state) || state == "UNKNOWN" ? "" : $"service {state}";

    private static string TrimForTray(string text) => text.Length <= 63 ? text : text[..60] + "...";

    private static string? QueryValue(string query, string name)
    {
        if (query.StartsWith("?"))
        {
            query = query[1..];
        }
        foreach (var part in query.Split('&', StringSplitOptions.RemoveEmptyEntries))
        {
            var pieces = part.Split('=', 2);
            var key = Uri.UnescapeDataString(pieces[0]);
            if (!key.Equals(name, StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }
            return pieces.Length == 2 ? Uri.UnescapeDataString(pieces[1]) : "";
        }
        return null;
    }

    private void WriteStatus(StatusSnapshot status)
    {
        try
        {
            var json = JsonSerializer.Serialize(status, new JsonSerializerOptions
            {
                WriteIndented = true,
                DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
            });
            File.WriteAllText(statusPath, json);
        }
        catch (Exception ex)
        {
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

    private static string MergeNoProxy(string? current)
    {
        var required = new[] { "127.0.0.1", "localhost", "api.qsdm.tech" };
        var parts = (current ?? "")
            .Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries)
            .ToList();
        foreach (var item in required)
        {
            if (!parts.Any(p => p.Equals(item, StringComparison.OrdinalIgnoreCase)))
            {
                parts.Add(item);
            }
        }
        return string.Join(",", parts);
    }
}

internal sealed record GuiSnapshot(bool? GatewayPublic, long? ValidatorHeight);

internal sealed record StatusSnapshot(
    bool ValidatorReady,
    int ValidatorProcesses,
    long? ValidatorHeight,
    bool MinerRunning,
    int MinerProcesses,
    string MinerServiceState,
    bool GatewayRunning,
    bool GatewayPublic,
    int GatewayProcesses,
    bool GuiRunning,
    int GuiProcesses,
    DateTime CheckedAt,
    string Error)
{
    public static StatusSnapshot FromError(string message) => new(
        false, 0, null, false, 0, "UNKNOWN", false, false, 0, false, 0, DateTime.Now, message);

    public QIconState Level
    {
        get
        {
            if (!string.IsNullOrWhiteSpace(Error) || !ValidatorReady || !MinerRunning)
            {
                return QIconState.Bad;
            }
            if (!GatewayRunning || !GatewayPublic || !GuiRunning)
            {
                return QIconState.Warn;
            }
            return QIconState.Ok;
        }
    }

    public string ShortSummary
    {
        get
        {
            if (!ValidatorReady)
            {
                return "validator not ready";
            }
            if (!MinerRunning)
            {
                return "miner stopped";
            }
            if (!GatewayRunning)
            {
                return "gateway stopped";
            }
            if (!GatewayPublic)
            {
                return "gateway local only";
            }
            return $"OK h{(ValidatorHeight?.ToString() ?? "-")}";
        }
    }

    public string StateKey => $"{Level}|{ValidatorReady}|{MinerRunning}|{GatewayRunning}|{GatewayPublic}|{GuiRunning}|{Error}";
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
