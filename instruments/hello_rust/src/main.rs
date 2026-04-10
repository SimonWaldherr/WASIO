// instruments/hello_rust/src/main.rs
//
// WASIO instrument – multilingual greeting service.
//
// Reads a JSON requestPayload from stdin (written by the WASIO host) and
// writes a JSON response to stdout.
//
// Supported query parameters:
//   name  – person to greet          (default: "World")
//   lang  – language code            (default: "en")
//   n     – number of greetings      (default: 1, max: 10)
//
// Supported languages: en, de, es, fr, it, pt, nl, pl, ja, zh
//
// No external crates – the entire impl uses std only, making the resulting
// WASM binary very small (< 30 KiB release).
//
// All error paths use eprintln! + return; – no unwrap(), no panics.

use std::io::{self, Read, Write};

fn main() {
    // Emit panic details to stderr so the host can surface them in logs.
    std::panic::set_hook(Box::new(|info| {
        let _ = io::stderr().write_all(format!("panic: {}\n", info).as_bytes());
    }));

    let mut input = String::new();
    if io::stdin().read_to_string(&mut input).is_err() {
        let _ = io::stdout().write_all(br#"{"error":"failed to read stdin"}"#);
        return;
    }

    let name = extract_param(&input, "name").unwrap_or_else(|| "World".to_string());
    let lang = extract_param(&input, "lang").unwrap_or_else(|| "en".to_string());
    let n: usize = extract_param(&input, "n")
        .and_then(|v| v.parse::<usize>().ok())
        .unwrap_or(1)
        .min(10)
        .max(1);

    let greeting = greet(&name, &lang);

    // Build the greetings JSON array without Vec<&str> borrows.
    let mut greetings_json = String::new();
    for i in 0..n {
        if i > 0 {
            greetings_json.push(',');
        }
        greetings_json.push('"');
        greetings_json.push_str(&greeting);
        greetings_json.push('"');
    }

    let out = format!(
        r#"{{"greeting":{},"greetings":[{}],"language":"{}","name":"{}","runtime":"Rust/WASM","n":{}}}"#,
        json_str(&greeting),
        greetings_json,
        lang,
        name,
        n
    );

    let _ = io::stdout().write_all(out.as_bytes());
}

fn greet(name: &str, lang: &str) -> String {
    let prefix = match lang {
        "de" => "Hallo",
        "es" => "¡Hola",
        "fr" => "Bonjour",
        "it" => "Ciao",
        "pt" => "Olá",
        "nl" => "Hoi",
        "pl" => "Cześć",
        "ja" => "こんにちは",
        "zh" => "你好",
        _    => "Hello",
    };
    let sep = match lang {
        "es" => ", ",
        "ja" | "zh" => "、",
        _ => ", ",
    };
    let suffix = match lang {
        "ja" | "zh" => "！",
        _ => "!",
    };
    format!("{}{}{}{}", prefix, sep, name, suffix)
}

/// Minimal JSON string escaping – no external crate needed.
fn json_str(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for ch in s.chars() {
        match ch {
            '"'  => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            c    => out.push(c),
        }
    }
    out.push('"');
    out
}

/// Extract a string value from `"params":{"key":"value"}` without serde.
fn extract_param(json: &str, key: &str) -> Option<String> {
    let params_marker = "\"params\":";
    let start = json.find(params_marker)? + params_marker.len();
    let params_substr = json[start..].trim_start();
    if !params_substr.starts_with('{') {
        return None;
    }

    let search = format!("\"{}\":", key);
    let key_pos = params_substr.find(&search)? + search.len();
    let value_substr = params_substr[key_pos..].trim_start();

    if !value_substr.starts_with('"') {
        return None;
    }
    let mut result = String::new();
    let mut escaped = false;
    for ch in value_substr[1..].chars() {
        if escaped {
            result.push(ch);
            escaped = false;
        } else if ch == '\\' {
            escaped = true;
        } else if ch == '"' {
            break;
        } else {
            result.push(ch);
        }
    }
    Some(result)
}
