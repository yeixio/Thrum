//! The Thrum desktop shell.
//!
//! The window hosts the same web UI as the browser version. This side adds
//! what a browser cannot do: it runs the bundled collector, lists
//! kubeconfig contexts, and keeps Thrum alive in the system tray.

use std::io::{BufRead, BufReader};
use std::net::{SocketAddr, TcpListener, TcpStream};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use serde::Serialize;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::TrayIconBuilder;
use tauri::{AppHandle, Emitter, Manager, RunEvent, State, WindowEvent};

const TRAY_ID: &str = "main";
const STARTUP_TIMEOUT: Duration = Duration::from_secs(20);

/// The running collector process, if any.
#[derive(Default)]
struct Collector(Mutex<Option<Child>>);

impl Collector {
    fn stop(&self) {
        if let Some(mut child) = self.0.lock().unwrap().take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

#[derive(Serialize)]
struct Connection {
    url: String,
    token: String,
}

/// The collector is bundled next to the app's own executable.
fn collector_path() -> Result<PathBuf, String> {
    let exe = std::env::current_exe().map_err(|e| e.to_string())?;
    let dir = exe
        .parent()
        .ok_or("the app executable has no parent directory")?;
    let name = if cfg!(windows) {
        "thrum-collector.exe"
    } else {
        "thrum-collector"
    };
    let path = dir.join(name);
    if path.exists() {
        Ok(path)
    } else {
        Err(format!(
            "the bundled collector is missing ({})",
            path.display()
        ))
    }
}

fn collector_command() -> Result<Command, String> {
    #[allow(unused_mut)] // only mutated on Windows
    let mut cmd = Command::new(collector_path()?);
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(0x0800_0000); // CREATE_NO_WINDOW
    }
    Ok(cmd)
}

/// A random token so that other local processes cannot read the collector.
fn random_token() -> Result<String, String> {
    let mut bytes = [0u8; 24];
    getrandom::fill(&mut bytes).map_err(|e| e.to_string())?;
    Ok(bytes.iter().map(|b| format!("{b:02x}")).collect())
}

fn free_port() -> Result<u16, String> {
    let listener = TcpListener::bind("127.0.0.1:0").map_err(|e| e.to_string())?;
    Ok(listener.local_addr().map_err(|e| e.to_string())?.port())
}

/// Lists kubeconfig contexts. Does not contact any cluster.
#[tauri::command(async)]
fn list_contexts() -> Result<serde_json::Value, String> {
    let out = collector_command()?
        .arg("--list-contexts")
        .output()
        .map_err(|e| format!("could not run the collector: {e}"))?;
    if !out.status.success() {
        return Err(String::from_utf8_lossy(&out.stderr).trim().to_string());
    }
    serde_json::from_slice(&out.stdout).map_err(|e| e.to_string())
}

/// Starts the collector for a kubeconfig context and waits until it serves.
#[tauri::command(async)]
fn start_collector(
    context: Option<String>,
    collector: State<'_, Collector>,
) -> Result<Connection, String> {
    collector.stop();

    let port = free_port()?;
    let token = random_token()?;
    let mut cmd = collector_command()?;
    cmd.arg("--listen")
        .arg(format!("127.0.0.1:{port}"))
        .env("THRUM_TOKEN", &token)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::piped());
    if let Some(ctx) = context.as_deref().filter(|c| !c.is_empty()) {
        cmd.arg("--context").arg(ctx);
    }
    let mut child = cmd
        .spawn()
        .map_err(|e| format!("could not start the collector: {e}"))?;

    // Keep the last few log lines so a failure can be explained, and keep
    // draining the pipe so the collector never blocks on a full buffer.
    let log = Arc::new(Mutex::new(Vec::<String>::new()));
    if let Some(stderr) = child.stderr.take() {
        let log = Arc::clone(&log);
        std::thread::spawn(move || {
            for line in BufReader::new(stderr).lines().map_while(Result::ok) {
                let mut log = log.lock().unwrap();
                if log.len() >= 20 {
                    log.remove(0);
                }
                log.push(line);
            }
        });
    }
    let last_log =
        |log: &Arc<Mutex<Vec<String>>>| log.lock().unwrap().last().cloned().unwrap_or_default();

    // The collector only starts serving once it has synced with the cluster.
    let addr: SocketAddr = ([127, 0, 0, 1], port).into();
    let started = Instant::now();
    loop {
        if TcpStream::connect_timeout(&addr, Duration::from_millis(200)).is_ok() {
            break;
        }
        if let Ok(Some(_)) = child.try_wait() {
            std::thread::sleep(Duration::from_millis(100));
            return Err(format!("the collector stopped: {}", last_log(&log)));
        }
        if started.elapsed() > STARTUP_TIMEOUT {
            let _ = child.kill();
            let _ = child.wait();
            return Err(format!(
                "could not reach the cluster within {} seconds. {}",
                STARTUP_TIMEOUT.as_secs(),
                last_log(&log)
            ));
        }
        std::thread::sleep(Duration::from_millis(150));
    }

    *collector.0.lock().unwrap() = Some(child);
    Ok(Connection {
        url: format!("http://127.0.0.1:{port}"),
        token,
    })
}

#[tauri::command]
fn stop_collector(collector: State<'_, Collector>) {
    collector.stop();
}

/// Shows the current health next to the tray icon.
#[tauri::command]
fn set_tray(app: AppHandle, health: Option<f64>, cluster: Option<String>) {
    let Some(tray) = app.tray_by_id(TRAY_ID) else {
        return;
    };
    match health {
        Some(h) => {
            let h = h.round().clamp(0.0, 100.0) as i64;
            let name = cluster
                .filter(|c| !c.is_empty())
                .unwrap_or_else(|| "cluster".into());
            let _ = tray.set_tooltip(Some(format!("Thrum: {name} health {h}")));
            let _ = tray.set_title(Some(h.to_string()));
        }
        None => {
            let _ = tray.set_tooltip(Some("Thrum"));
            let _ = tray.set_title(None::<&str>);
        }
    }
}

fn show_main_window(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.unminimize();
        let _ = window.set_focus();
    }
}

fn build_tray(app: &AppHandle) -> tauri::Result<()> {
    let show = MenuItem::with_id(app, "show", "Show Thrum", true, None::<&str>)?;
    let toggle = MenuItem::with_id(
        app,
        "toggle-sound",
        "Start or stop sound",
        true,
        None::<&str>,
    )?;
    let quit = MenuItem::with_id(app, "quit", "Quit Thrum", true, None::<&str>)?;
    let menu = Menu::with_items(
        app,
        &[&show, &toggle, &PredefinedMenuItem::separator(app)?, &quit],
    )?;

    let mut tray = TrayIconBuilder::with_id(TRAY_ID)
        .tooltip("Thrum")
        .menu(&menu)
        .on_menu_event(|app, event| match event.id().as_ref() {
            "show" => show_main_window(app),
            "toggle-sound" => {
                let _ = app.emit("tray-toggle-sound", ());
            }
            "quit" => app.exit(0),
            _ => {}
        });
    if let Some(icon) = app.default_window_icon() {
        tray = tray.icon(icon.clone());
    }
    tray.build(app)?;
    Ok(())
}

pub fn run() {
    let app = tauri::Builder::default()
        .manage(Collector::default())
        .invoke_handler(tauri::generate_handler![
            list_contexts,
            start_collector,
            stop_collector,
            set_tray
        ])
        .setup(|app| {
            build_tray(app.handle())?;
            Ok(())
        })
        .on_window_event(|window, event| {
            // Closing the window keeps the sound going. Quit from the tray.
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .build(tauri::generate_context!())
        .expect("error while building Thrum");

    app.run(|app, event| {
        if let RunEvent::Exit = event {
            app.state::<Collector>().stop();
        }
    });
}
