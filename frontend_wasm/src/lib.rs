use wasm_bindgen::prelude::*;
use std::collections::HashMap;
use serde::{Serialize, Deserialize};

#[derive(Serialize, Deserialize, Clone)]
#[serde(rename_all = "camelCase")]
pub struct Aircraft {
    pub id: String,
    #[serde(default)]
    pub heading: f64,
    #[serde(default)]
    pub speed: f64,
    #[serde(default)]
    pub altitude: f64,
    #[serde(default)]
    pub lat: f64,
    #[serde(default)]
    pub lng: f64,
    #[serde(default)]
    pub call_sign: Option<String>,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default)]
    pub updated_at: Option<i64>,

    #[serde(default)]
    pub start_lng: f64,
    #[serde(default)]
    pub start_lat: f64,
    #[serde(default)]
    pub current_lng: f64,
    #[serde(default)]
    pub current_lat: f64,
    #[serde(default)]
    pub target_lng: f64,
    #[serde(default)]
    pub target_lat: f64,
    #[serde(default)]
    pub start_time: f64,
}

#[wasm_bindgen]
pub struct RadarEngine {
    targets: HashMap<String, Aircraft>,
    data_buffer: Vec<f64>, // Layout: [lng, lat, heading, id_hash, ...]
}

#[wasm_bindgen]
impl RadarEngine {
    #[wasm_bindgen(constructor)]
    pub fn new() -> Self {
        #[cfg(feature = "console_error_panic_hook")]
        console_error_panic_hook::set_once();
        
        Self { 
            targets: HashMap::new(),
            data_buffer: Vec::new(),
        }
    }

    pub fn update_targets(&mut self, added: JsValue, updated: JsValue, removed: JsValue, now: f64) {
        let added_vec: Vec<Aircraft> = serde_wasm_bindgen::from_value(added).unwrap_or_default();
        let updated_vec: Vec<Aircraft> = serde_wasm_bindgen::from_value(updated).unwrap_or_default();
        let removed_vec: Vec<String> = serde_wasm_bindgen::from_value(removed).unwrap_or_default();

        for mut ac in added_vec {
            ac.start_lng = ac.lng; ac.start_lat = ac.lat;
            ac.current_lng = ac.lng; ac.current_lat = ac.lat;
            ac.target_lng = ac.lng; ac.target_lat = ac.lat;
            ac.start_time = now;
            self.targets.insert(ac.id.clone(), ac);
        }

        for ac in updated_vec {
            if let Some(old) = self.targets.get_mut(&ac.id) {
                old.start_lng = old.current_lng;
                old.start_lat = old.current_lat;
                old.target_lng = ac.lng;
                old.target_lat = ac.lat;
                old.heading = ac.heading;
                old.speed = ac.speed;
                old.altitude = ac.altitude;
                old.call_sign = ac.call_sign;
                old.source = ac.source;
                old.updated_at = ac.updated_at;
                old.start_time = now;
            }
        }

        for id in removed_vec {
            self.targets.remove(&id);
        }
    }

    pub fn tick(&mut self, now: f64, interval: f64) {
        for d in self.targets.values_mut() {
            let elapsed = now - d.start_time;
            let t = (elapsed / interval).min(1.0).max(0.0);
            d.current_lng = d.start_lng + (d.target_lng - d.start_lng) * t;
            d.current_lat = d.start_lat + (d.target_lat - d.start_lat) * t;
        }
    }

    pub fn get_data_ptr(&mut self) -> *const f64 {
        self.data_buffer.clear();
        for ac in self.targets.values() {
            self.data_buffer.push(ac.current_lng);
            self.data_buffer.push(ac.current_lat);
            self.data_buffer.push(ac.heading);
            self.data_buffer.push(self.hash_id(&ac.id));
        }
        self.data_buffer.as_ptr()
    }

    pub fn get_total_count(&self) -> usize {
        self.targets.len()
    }

    fn hash_id(&self, id: &str) -> f64 {
        let mut h: u32 = 0;
        for b in id.as_bytes() {
            h = h.wrapping_mul(31).wrapping_add(*b as u32);
        }
        h as f64
    }
}
