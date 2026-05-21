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

#[derive(Serialize)]
struct GeoJson {
    #[serde(rename = "type")]
    type_field: &'static str,
    features: Vec<GeoJsonFeature>,
}

#[derive(Serialize)]
struct GeoJsonFeature {
    #[serde(rename = "type")]
    type_field: &'static str,
    geometry: Geometry,
    properties: Properties,
}

#[derive(Serialize)]
struct Geometry {
    #[serde(rename = "type")]
    type_field: &'static str,
    coordinates: [f64; 2],
}

#[derive(Serialize)]
struct Properties {
    id: String,
    heading: f64,
}

#[wasm_bindgen]
pub struct RadarEngine {
    targets: HashMap<String, Aircraft>,
}

#[wasm_bindgen]
impl RadarEngine {
    #[wasm_bindgen(constructor)]
    pub fn new() -> Self {
        #[cfg(feature = "console_error_panic_hook")]
        console_error_panic_hook::set_once();
        
        Self { targets: HashMap::new() }
    }

    pub fn update_targets(&mut self, added: JsValue, updated: JsValue, removed: JsValue, now: f64) {
        let added_vec: Vec<Aircraft> = serde_wasm_bindgen::from_value(added).unwrap_or_else(|e| {
            web_sys::console::error_1(&format!("WASM: added_vec deserialization error: {}", e).into());
            vec![]
        });
        let updated_vec: Vec<Aircraft> = serde_wasm_bindgen::from_value(updated).unwrap_or_else(|e| {
            web_sys::console::error_1(&format!("WASM: updated_vec deserialization error: {}", e).into());
            vec![]
        });
        let removed_vec: Vec<String> = serde_wasm_bindgen::from_value(removed).unwrap_or_default();

        if !added_vec.is_empty() || !updated_vec.is_empty() || !removed_vec.is_empty() {
             web_sys::console::log_1(&format!("WASM: Update targets - Added: {}, Updated: {}, Removed: {}", added_vec.len(), updated_vec.len(), removed_vec.len()).into());
        }

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
            } else {
                let mut new_ac = ac.clone();
                new_ac.start_lng = new_ac.lng; new_ac.start_lat = new_ac.lat;
                new_ac.current_lng = new_ac.lng; new_ac.current_lat = new_ac.lat;
                new_ac.target_lng = new_ac.lng; new_ac.target_lat = new_ac.lat;
                new_ac.start_time = now;
                self.targets.insert(new_ac.id.clone(), new_ac);
            }
        }

        for id in removed_vec {
            self.targets.remove(&id);
        }
    }

    pub fn tick(&mut self, now: f64, interval: f64) -> JsValue {
        for d in self.targets.values_mut() {
            let elapsed = now - d.start_time;
            let t = (elapsed / interval).min(1.0).max(0.0);
            d.current_lng = d.start_lng + (d.target_lng - d.start_lng) * t;
            d.current_lat = d.start_lat + (d.target_lat - d.start_lat) * t;
        }

        let features: Vec<GeoJsonFeature> = self.targets.values().map(|d| {
            GeoJsonFeature {
                type_field: "Feature",
                geometry: Geometry {
                    type_field: "Point",
                    coordinates: [d.current_lng, d.current_lat],
                },
                properties: Properties {
                    id: d.id.clone(),
                    heading: d.heading,
                },
            }
        }).collect();

        let geojson = GeoJson {
            type_field: "FeatureCollection",
            features,
        };

        serde_wasm_bindgen::to_value(&geojson).unwrap()
    }

    pub fn get_total_count(&self) -> usize {
        self.targets.len()
    }
}
