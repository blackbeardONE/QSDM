/**
 * WebGL panels for the operator dashboard.
 *
 *   Left  — live libp2p peer topology from /api/topology
 *   Right — Phase-3 parent mesh reference geometry from /api/mesh3d-viz
 *
 * three.js is vendored under /static/vendor/ so the panels keep working on
 * validator hosts with no outbound internet and under a script-src 'self' CSP.
 * See vendor/README.md.
 *
 * Structure: palette -> procedural textures -> material library -> geometry
 * helpers -> model factories -> lighting rig -> render pipeline -> link/flow
 * VFX -> panel controllers -> data polling -> diagnostics.
 */
import * as THREE from './vendor/three.module.min.js';
import { OrbitControls } from './vendor/OrbitControls.js';

const fetchOpts = { credentials: 'include', headers: { Accept: 'application/json' } };

const REDUCED_MOTION = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

const PALETTE = {
    bg: 0x0a1017,
    self: 0x4a9eff,
    selfCore: 0x63b4f5,
    peer: 0x7ed321,
    degraded: 0xf5a623,
    offline: 0x46535f,
    offlineHulk: 0x2b343e,
    parent: 0x6abf4f,
    dependency: 0x4a9eff,
    adjacent: 0x5d7183,
    grid: 0x16222f,
    gridHot: 0x3d5a74,
    trim: 0xc4d4e4,
    hull: 0x3f7fbf,
};

/* ------------------------------------------------------------------ *
 * Procedural textures
 * ------------------------------------------------------------------ */

const textureCache = new Map();

function cachedTexture(key, build) {
    if (!textureCache.has(key)) {
        const tex = build();
        tex.colorSpace = THREE.SRGBColorSpace;
        textureCache.set(key, tex);
    }
    return textureCache.get(key);
}

/** Soft radial falloff used for halos, flow pulses and the starfield. */
function glowTexture() {
    return cachedTexture('glow', () => {
        const size = 128;
        const c = document.createElement('canvas');
        c.width = size;
        c.height = size;
        const ctx = c.getContext('2d');
        const g = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
        g.addColorStop(0, 'rgba(255,255,255,1)');
        g.addColorStop(0.25, 'rgba(255,255,255,0.55)');
        g.addColorStop(0.6, 'rgba(255,255,255,0.12)');
        g.addColorStop(1, 'rgba(255,255,255,0)');
        ctx.fillStyle = g;
        ctx.fillRect(0, 0, size, size);
        return new THREE.CanvasTexture(c);
    });
}

/**
 * Panel lines and wear. Kept light because `map` multiplies `color` — a dark
 * map would crush the material to black no matter how the lights are set.
 */
function panelTexture() {
    return cachedTexture('panel', () => {
        const size = 256;
        const c = document.createElement('canvas');
        c.width = size;
        c.height = size;
        const ctx = c.getContext('2d');
        ctx.fillStyle = '#cdd8e3';
        ctx.fillRect(0, 0, size, size);
        ctx.strokeStyle = 'rgba(38,58,78,0.42)';
        ctx.lineWidth = 1;
        for (let i = 0; i <= size; i += 32) {
            ctx.beginPath();
            ctx.moveTo(i + 0.5, 0);
            ctx.lineTo(i + 0.5, size);
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(0, i + 0.5);
            ctx.lineTo(size, i + 0.5);
            ctx.stroke();
        }
        ctx.strokeStyle = 'rgba(30,48,66,0.55)';
        ctx.lineWidth = 2;
        ctx.strokeRect(16, 16, size - 32, size - 32);
        ctx.fillStyle = 'rgba(46,66,88,0.22)';
        for (let i = 0; i < 30; i += 1) {
            ctx.fillRect(Math.random() * size, Math.random() * size, 8 + Math.random() * 26, 2);
        }
        const tex = new THREE.CanvasTexture(c);
        tex.wrapS = THREE.RepeatWrapping;
        tex.wrapT = THREE.RepeatWrapping;
        tex.anisotropy = 4;
        return tex;
    });
}

/* ------------------------------------------------------------------ *
 * Material library — named roles, shared across every mesh
 * ------------------------------------------------------------------ */

function shared(material) {
    material.userData.shared = true;
    return material;
}

const MAT = {
    /** Plated steel for larger surfaces where the panel detail is legible. */
    hullPlated: shared(new THREE.MeshStandardMaterial({
        color: 0xa9bed2, roughness: 0.46, metalness: 0.5, map: panelTexture(),
    })),
    trim: shared(new THREE.MeshStandardMaterial({ color: PALETTE.trim, roughness: 0.32, metalness: 0.72 })),
    /** Faceted canopy over the local core. Avoids `transmission`, which forces
     *  an extra render-target pass per frame — too costly for an always-on panel. */
    canopy: shared(new THREE.MeshStandardMaterial({
        color: 0x74b4ec, roughness: 0.14, metalness: 0.1, transparent: true,
        opacity: 0.16, side: THREE.DoubleSide, depthWrite: false,
    })),
    hull: shared(new THREE.MeshStandardMaterial({
        color: PALETTE.hull, roughness: 0.4, metalness: 0.1, transparent: true,
        opacity: 0.14, side: THREE.DoubleSide, depthWrite: false,
    })),
};

/** Peer bodies: neutral machined metal plus a faint bleed of the state colour. */
const bodyCache = new Map();
function bodyMaterial(tint) {
    if (!bodyCache.has(tint)) {
        bodyCache.set(tint, shared(new THREE.MeshStandardMaterial({
            color: 0x8ba1b6, roughness: 0.38, metalness: 0.68,
            emissive: tint, emissiveIntensity: 0.28,
        })));
    }
    return bodyCache.get(tint);
}

/** Emissive signal materials are per-role so state reads by colour and glow. */
const emissiveCache = new Map();
function emissiveSignal(color, intensity = 1.4) {
    const key = `${color}:${intensity}`;
    if (!emissiveCache.has(key)) {
        emissiveCache.set(key, shared(new THREE.MeshStandardMaterial({
            color: 0x16202b, emissive: color, emissiveIntensity: intensity,
            roughness: 0.35, metalness: 0.15,
        })));
    }
    return emissiveCache.get(key);
}

const lineCache = new Map();
function lineMaterial(color, opacity) {
    const key = `${color}:${opacity}`;
    if (!lineCache.has(key)) {
        lineCache.set(key, shared(new THREE.LineBasicMaterial({ color, transparent: true, opacity })));
    }
    return lineCache.get(key);
}

const tubeCache = new Map();
function tubeMaterial(color, opacity) {
    const key = `${color}:${opacity}`;
    if (!tubeCache.has(key)) {
        tubeCache.set(key, shared(new THREE.MeshBasicMaterial({
            color, transparent: true, opacity, blending: THREE.AdditiveBlending, depthWrite: false,
        })));
    }
    return tubeCache.get(key);
}

const haloCache = new Map();
function haloMaterial(color, opacity) {
    const key = `${color}:${opacity}`;
    if (!haloCache.has(key)) {
        haloCache.set(key, shared(new THREE.SpriteMaterial({
            map: glowTexture(), color, transparent: true, opacity,
            blending: THREE.AdditiveBlending, depthWrite: false, fog: false,
        })));
    }
    return haloCache.get(key);
}

/* ------------------------------------------------------------------ *
 * Shared geometries
 * ------------------------------------------------------------------ */

function sharedGeometry(geometry) {
    geometry.userData.shared = true;
    return geometry;
}

const GEO = {
    coreInner: sharedGeometry(new THREE.IcosahedronGeometry(7, 1)),
    coreShell: sharedGeometry(new THREE.IcosahedronGeometry(11.5, 1)),
    gimbal: sharedGeometry(new THREE.TorusGeometry(17, 0.8, 8, 64)),
    baseRing: sharedGeometry(new THREE.TorusGeometry(23, 0.7, 8, 72)),
    peerBody: sharedGeometry(new THREE.OctahedronGeometry(6.4, 0)),
    peerRing: sharedGeometry(new THREE.TorusGeometry(6.9, 0.5, 8, 32)),
    peerCore: sharedGeometry(new THREE.OctahedronGeometry(4.2, 0)),
    peerCage: sharedGeometry(new THREE.OctahedronGeometry(6.6, 0)),
    crystal: sharedGeometry(new THREE.DodecahedronGeometry(8.5, 0)),
    crystalBelt: sharedGeometry(new THREE.TorusGeometry(9.2, 0.55, 8, 36)),
};

/* ------------------------------------------------------------------ *
 * Geometry helpers
 * ------------------------------------------------------------------ */

/**
 * Concatenates indexed position/normal/uv geometries into one buffer so every
 * link of a given kind costs a single draw call. Avoids pulling in the
 * BufferGeometryUtils addon.
 */
function mergeGeometries(geometries) {
    let vertexCount = 0;
    let indexCount = 0;
    for (const g of geometries) {
        vertexCount += g.attributes.position.count;
        indexCount += g.index ? g.index.count : g.attributes.position.count;
    }
    const position = new Float32Array(vertexCount * 3);
    const normal = new Float32Array(vertexCount * 3);
    const uv = new Float32Array(vertexCount * 2);
    const index = new Uint32Array(indexCount);

    let vOffset = 0;
    let iOffset = 0;
    for (const g of geometries) {
        position.set(g.attributes.position.array, vOffset * 3);
        if (g.attributes.normal) normal.set(g.attributes.normal.array, vOffset * 3);
        if (g.attributes.uv) uv.set(g.attributes.uv.array, vOffset * 2);
        if (g.index) {
            const src = g.index.array;
            for (let k = 0; k < src.length; k += 1) index[iOffset + k] = src[k] + vOffset;
            iOffset += src.length;
        } else {
            for (let k = 0; k < g.attributes.position.count; k += 1) index[iOffset + k] = k + vOffset;
            iOffset += g.attributes.position.count;
        }
        vOffset += g.attributes.position.count;
        g.dispose();
    }

    const merged = new THREE.BufferGeometry();
    merged.setAttribute('position', new THREE.BufferAttribute(position, 3));
    merged.setAttribute('normal', new THREE.BufferAttribute(normal, 3));
    merged.setAttribute('uv', new THREE.BufferAttribute(uv, 2));
    merged.setIndex(new THREE.BufferAttribute(index, 1));
    return merged;
}

/** Links bow away from the origin so overlapping edges stay separable in 3D. */
function linkCurve(a, b) {
    const mid = a.clone().add(b).multiplyScalar(0.5);
    const bow = mid.lengthSq() < 1e-3 ? new THREE.Vector3(0, 1, 0) : mid.clone().normalize();
    const lift = a.distanceTo(b) * 0.16;
    return new THREE.QuadraticBezierCurve3(a.clone(), mid.add(bow.multiplyScalar(lift)), b.clone());
}

/** Deterministic point on a sphere, so a peer keeps its slot between polls. */
function hashPoint(id, radius) {
    let h1 = 2166136261;
    let h2 = 2166136261 ^ 0x5f3759df;
    for (let i = 0; i < id.length; i += 1) {
        h1 = Math.imul(h1 ^ id.charCodeAt(i), 16777619);
        h2 = Math.imul(h2 ^ id.charCodeAt(id.length - 1 - i), 2246822519);
    }
    const u = ((h1 >>> 8) % 100000) / 100000;
    const v = ((h2 >>> 8) % 100000) / 100000;
    const y = 1 - 2 * u;
    const r = Math.sqrt(Math.max(0, 1 - y * y));
    const theta = 2 * Math.PI * v;
    return new THREE.Vector3(Math.cos(theta) * r * radius, y * radius * 0.72, Math.sin(theta) * r * radius);
}

function disposeTree(object) {
    object.traverse((child) => {
        if (child.geometry && !child.geometry.userData.shared) child.geometry.dispose();
        const materials = Array.isArray(child.material) ? child.material : [child.material];
        materials.forEach((m) => {
            if (m && !m.userData.shared) m.dispose();
        });
    });
}

/* ------------------------------------------------------------------ *
 * Model factories
 * ------------------------------------------------------------------ */

/**
 * Local node: layered core, counter-rotating gimbals and a hex contact pad.
 * Reads as "this machine" rather than one more sphere in the graph.
 */
function makeCoreNode(scale = 1) {
    const root = new THREE.Group();
    root.name = 'localNode';

    const core = new THREE.Mesh(GEO.coreInner, emissiveSignal(PALETTE.selfCore, 1.1));
    core.name = 'coreInner';
    root.add(core);

    const shell = new THREE.Mesh(GEO.coreShell, MAT.canopy);
    shell.name = 'coreShell';
    root.add(shell);

    const gimbals = new THREE.Group();
    gimbals.name = 'gimbals';
    const tilts = [
        [0, 0, 0],
        [Math.PI / 2, 0, Math.PI / 5],
        [Math.PI / 3, Math.PI / 2, 0],
    ];
    tilts.forEach((rot, i) => {
        const ring = new THREE.Mesh(GEO.gimbal, MAT.trim);
        ring.rotation.set(rot[0], rot[1], rot[2]);
        ring.scale.setScalar(1 - i * 0.14);
        ring.name = `gimbal${i}`;
        gimbals.add(ring);
    });
    root.add(gimbals);

    // A lit ring grounds the node without dropping an opaque slab into frame.
    const base = new THREE.Mesh(GEO.baseRing, emissiveSignal(PALETTE.self, 0.7));
    base.name = 'baseRing';
    base.rotation.x = Math.PI / 2;
    base.position.y = -21;
    root.add(base);

    const halo = new THREE.Sprite(haloMaterial(PALETTE.self, 0.22));
    halo.name = 'halo';
    halo.scale.setScalar(38);
    root.add(halo);

    root.scale.setScalar(scale);
    root.userData.spin = gimbals;
    root.userData.restScale = scale;
    return root;
}

/**
 * Peer forms differ by silhouette as well as colour: a trimmed octahedron when
 * healthy, a banded one when degraded, an open wireframe cage when offline.
 */
const offlineCage = shared(new THREE.MeshBasicMaterial({
    color: PALETTE.offline, wireframe: true, transparent: true, opacity: 0.55,
}));
const offlineHulk = shared(new THREE.MeshStandardMaterial({
    color: PALETTE.offlineHulk, roughness: 0.95, metalness: 0.1,
}));

function makePeerNode(state) {
    const root = new THREE.Group();
    root.name = `peer-${state}`;

    if (state === 'offline') {
        const hulk = new THREE.Mesh(GEO.peerCore, offlineHulk);
        hulk.name = 'hulk';
        root.add(hulk);
        const cage = new THREE.Mesh(GEO.peerCage, offlineCage);
        cage.name = 'cage';
        root.add(cage);
        return root;
    }

    const color = state === 'degraded' ? PALETTE.degraded : PALETTE.peer;

    const body = new THREE.Mesh(GEO.peerBody, bodyMaterial(color));
    body.name = 'body';
    root.add(body);

    // The state ring is the primary read: a lit band is legible at glance
    // distance where a small status dot is not.
    const ring = new THREE.Mesh(GEO.peerRing, emissiveSignal(color, 0.85));
    ring.name = 'stateRing';
    ring.rotation.x = Math.PI / 2;
    root.add(ring);

    if (state === 'degraded') {
        const band = new THREE.Mesh(GEO.peerRing, emissiveSignal(PALETTE.degraded, 0.95));
        band.name = 'warningBand';
        band.rotation.set(Math.PI / 2, 0, Math.PI / 3.2);
        band.scale.setScalar(1.16);
        root.add(band);
    }

    const halo = new THREE.Sprite(haloMaterial(color, 0.4));
    halo.name = 'halo';
    halo.scale.setScalar(30);
    root.add(halo);

    return root;
}

/** Parent cells in the reference mesh: faceted crystal with an equatorial belt. */
function makeParentCell(color) {
    const root = new THREE.Group();
    root.name = 'parentCell';

    const body = new THREE.Mesh(GEO.crystal, MAT.hullPlated);
    body.name = 'crystal';
    root.add(body);

    const belt = new THREE.Mesh(GEO.crystalBelt, emissiveSignal(color, 1.2));
    belt.name = 'belt';
    belt.rotation.x = Math.PI / 2;
    root.add(belt);

    const halo = new THREE.Sprite(haloMaterial(color, 0.34));
    halo.name = 'halo';
    halo.scale.setScalar(38);
    root.add(halo);

    return root;
}

/** Radar-style ground plane: concentric rings plus spokes, fading outward. */
function makeRadarFloor(radius, y) {
    const positions = [];
    const colors = [];
    const hot = new THREE.Color(PALETTE.gridHot);
    const cold = new THREE.Color(PALETTE.grid);

    const rings = 5;
    for (let r = 1; r <= rings; r += 1) {
        const rr = (radius * r) / rings;
        const segments = 96;
        const tint = cold.clone().lerp(hot, 1 - r / rings);
        for (let s = 0; s < segments; s += 1) {
            const a0 = (s / segments) * Math.PI * 2;
            const a1 = ((s + 1) / segments) * Math.PI * 2;
            positions.push(Math.cos(a0) * rr, y, Math.sin(a0) * rr, Math.cos(a1) * rr, y, Math.sin(a1) * rr);
            colors.push(tint.r, tint.g, tint.b, tint.r, tint.g, tint.b);
        }
    }
    for (let s = 0; s < 16; s += 1) {
        const a = (s / 16) * Math.PI * 2;
        positions.push(0, y, 0, Math.cos(a) * radius, y, Math.sin(a) * radius);
        colors.push(hot.r, hot.g, hot.b, cold.r * 0.4, cold.g * 0.4, cold.b * 0.4);
    }

    const geometry = new THREE.BufferGeometry();
    geometry.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
    geometry.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
    const floor = new THREE.LineSegments(geometry, shared(new THREE.LineBasicMaterial({
        vertexColors: true, transparent: true, opacity: 0.95,
    })));
    floor.name = 'radarFloor';
    return floor;
}

/** Unfogged backdrop points so the panel has depth behind the graph. */
function makeStarfield(count, inner, outer) {
    const positions = new Float32Array(count * 3);
    for (let i = 0; i < count; i += 1) {
        const dir = new THREE.Vector3(Math.random() * 2 - 1, Math.random() * 2 - 1, Math.random() * 2 - 1).normalize();
        const r = inner + Math.random() * (outer - inner);
        positions[i * 3] = dir.x * r;
        positions[i * 3 + 1] = dir.y * r;
        positions[i * 3 + 2] = dir.z * r;
    }
    const geometry = new THREE.BufferGeometry();
    geometry.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    const points = new THREE.Points(geometry, shared(new THREE.PointsMaterial({
        color: 0x5f7d9c, size: 2.4, sizeAttenuation: true, map: glowTexture(),
        transparent: true, opacity: 0.5, depthWrite: false, blending: THREE.AdditiveBlending, fog: false,
    })));
    points.name = 'starfield';
    return points;
}

/* ------------------------------------------------------------------ *
 * Lighting rig
 * ------------------------------------------------------------------ */

/**
 * Metals need something to reflect. Without an environment map a
 * high-metalness MeshStandardMaterial renders almost black, which is what made
 * the trim and hull plating disappear. This builds a tiny procedural equirect
 * gradient (sky / horizon / floor plus one key hotspot) and pre-filters it with
 * PMREMGenerator, so no external HDRI or addon is required.
 */
function buildEnvironment(renderer) {
    const canvas = document.createElement('canvas');
    canvas.width = 128;
    canvas.height = 64;
    const ctx = canvas.getContext('2d');

    const sky = ctx.createLinearGradient(0, 0, 0, 64);
    sky.addColorStop(0, '#9fc2e4');
    sky.addColorStop(0.4, '#4a6a8c');
    sky.addColorStop(0.58, '#243141');
    sky.addColorStop(1, '#0b1119');
    ctx.fillStyle = sky;
    ctx.fillRect(0, 0, 128, 64);

    const key = ctx.createRadialGradient(88, 14, 0, 88, 14, 34);
    key.addColorStop(0, 'rgba(226,240,255,0.95)');
    key.addColorStop(1, 'rgba(226,240,255,0)');
    ctx.fillStyle = key;
    ctx.fillRect(0, 0, 128, 64);

    const rim = ctx.createRadialGradient(20, 24, 0, 20, 24, 26);
    rim.addColorStop(0, 'rgba(74,158,255,0.55)');
    rim.addColorStop(1, 'rgba(74,158,255,0)');
    ctx.fillStyle = rim;
    ctx.fillRect(0, 0, 128, 64);

    const source = new THREE.CanvasTexture(canvas);
    source.mapping = THREE.EquirectangularReflectionMapping;
    source.colorSpace = THREE.SRGBColorSpace;

    const pmrem = new THREE.PMREMGenerator(renderer);
    const environment = pmrem.fromEquirectangular(source).texture;
    pmrem.dispose();
    source.dispose();
    return environment;
}

function buildLightingRig(scene) {
    const rig = new THREE.Group();
    rig.name = 'lightingRig';

    const key = new THREE.DirectionalLight(0xcfe4ff, 1.5);
    key.position.set(60, 110, 80);
    rig.add(key);

    const fill = new THREE.DirectionalLight(0x2f4a6b, 0.9);
    fill.position.set(-90, -30, -60);
    rig.add(fill);

    const rim = new THREE.DirectionalLight(0x63b7ff, 1.1);
    rim.position.set(-40, 60, -120);
    rig.add(rim);

    rig.add(new THREE.HemisphereLight(0x2a4358, 0x070b10, 0.7));
    scene.add(rig);
    return rig;
}

/* ------------------------------------------------------------------ *
 * Render pipeline
 * ------------------------------------------------------------------ */

function createViz(container, options) {
    const width = container.clientWidth || 600;
    const height = container.clientHeight || 420;

    const scene = new THREE.Scene();
    scene.background = new THREE.Color(PALETTE.bg);
    scene.fog = new THREE.Fog(PALETTE.bg, 260, 780);

    const camera = new THREE.PerspectiveCamera(50, width / height, 0.5, 3000);
    camera.position.set(options.camera[0], options.camera[1], options.camera[2]);

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: false, powerPreference: 'high-performance' });
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, window.innerWidth < 720 ? 1.5 : 2));
    renderer.setSize(width, height);
    renderer.outputColorSpace = THREE.SRGBColorSpace;
    renderer.toneMapping = THREE.ACESFilmicToneMapping;
    renderer.toneMappingExposure = 1.0;

    scene.environment = buildEnvironment(renderer);
    scene.environmentIntensity = 1.35;

    container.textContent = '';
    container.style.position = 'relative';
    container.appendChild(renderer.domElement);
    renderer.domElement.style.display = 'block';
    renderer.domElement.style.borderRadius = '3px';

    const controls = new OrbitControls(camera, renderer.domElement);
    if (options.target) controls.target.set(options.target[0], options.target[1], options.target[2]);
    controls.enableDamping = true;
    controls.dampingFactor = 0.07;
    controls.rotateSpeed = 0.65;
    controls.minDistance = 70;
    controls.maxDistance = 900;
    controls.autoRotate = !REDUCED_MOTION;
    controls.autoRotateSpeed = options.autoRotateSpeed ?? 0.35;
    controls.addEventListener('start', () => { controls.autoRotate = false; });

    buildLightingRig(scene);

    const world = new THREE.Group();
    world.name = 'world';
    scene.add(world);

    const nodeLayer = new THREE.Group();
    nodeLayer.name = 'nodeLayer';
    world.add(nodeLayer);

    const linkLayer = new THREE.Group();
    linkLayer.name = 'linkLayer';
    world.add(linkLayer);

    scene.add(makeStarfield(220, 700, 1400));

    const tooltip = document.createElement('div');
    tooltip.className = 'viz-3d-tip';
    tooltip.setAttribute('role', 'status');
    container.appendChild(tooltip);

    const hint = document.createElement('div');
    hint.className = 'viz-3d-hint';
    hint.textContent = options.hint;
    container.appendChild(hint);

    return {
        container, scene, camera, renderer, controls, world, nodeLayer, linkLayer,
        tooltip, hint, flow: null, hoverTargets: [], contextLost: false,
    };
}

function resizeViz(viz) {
    const width = viz.container.clientWidth;
    const height = viz.container.clientHeight || 420;
    if (width < 2 || height < 2) return;
    viz.camera.aspect = width / height;
    viz.camera.updateProjectionMatrix();
    viz.renderer.setPixelRatio(Math.min(window.devicePixelRatio, window.innerWidth < 720 ? 1.5 : 2));
    viz.renderer.setSize(width, height);
}

/* ------------------------------------------------------------------ *
 * Links and flow VFX
 * ------------------------------------------------------------------ */

/**
 * Rebuilds the link layer. Each visual kind is merged into one draw call and
 * "active" curves seed the flow particles so traffic direction is legible.
 */
function buildLinks(viz, links) {
    while (viz.linkLayer.children.length) {
        const child = viz.linkLayer.children[0];
        viz.linkLayer.remove(child);
        disposeTree(child);
    }

    const groups = new Map();
    const seeds = [];
    const peakTraffic = links.reduce((max, link) => Math.max(max, link.traffic || 0), 0);

    links.forEach((link) => {
        const curve = linkCurve(link.from, link.to);
        const key = `${link.color}:${link.opacity}:${link.radius}`;
        if (!groups.has(key)) groups.set(key, { color: link.color, opacity: link.opacity, geometries: [] });
        groups.get(key).geometries.push(new THREE.TubeGeometry(curve, 22, link.radius, 5, false));
        if (link.active) {
            seeds.push({ curve, speed: flowSpeed(link.latencyMs), pulses: flowPulses(link.traffic, peakTraffic) });
        }
    });

    let drawCalls = 0;
    groups.forEach((group) => {
        const mesh = new THREE.Mesh(mergeGeometries(group.geometries), tubeMaterial(group.color, group.opacity));
        mesh.name = 'linkBundle';
        viz.linkLayer.add(mesh);
        drawCalls += 1;
    });

    buildFlow(viz, seeds);
    return { bundles: drawCalls, curves: seeds.length };
}

/** Pulse travel time reads as round-trip latency: quick pulse, responsive peer. */
function flowSpeed(latencyMs) {
    if (typeof latencyMs !== 'number' || !Number.isFinite(latencyMs)) return 0.22;
    const clamped = Math.min(Math.max(latencyMs, 5), 400);
    return 0.45 - ((clamped - 5) / 395) * 0.37;
}

/** Pulse density reads as message volume, normalised against the busiest link. */
function flowPulses(traffic, peak) {
    if (!peak || typeof traffic !== 'number' || traffic <= 0) return 1;
    return 1 + Math.round((Math.min(traffic, peak) / peak) * 3);
}

/**
 * Traffic pulses riding the active links. One Points draw call for the whole
 * panel; positions are advanced on the CPU each frame.
 */
const MAX_PULSES = 260;

function buildFlow(viz, seeds) {
    if (viz.flow) {
        viz.world.remove(viz.flow.points);
        viz.flow.points.geometry.dispose();
        viz.flow = null;
    }
    if (!seeds.length || REDUCED_MOTION) return;

    const state = [];
    const budget = Math.max(1, Math.floor(MAX_PULSES / seeds.length));
    seeds.forEach((seed) => {
        const count = Math.min(seed.pulses, budget);
        for (let i = 0; i < count; i += 1) {
            // Even phase offsets keep a busy link reading as a steady stream
            // rather than a random flicker.
            state.push({ curve: seed.curve, t: (i + Math.random() * 0.4) / count, speed: seed.speed });
        }
    });
    if (!state.length) return;
    const positions = new Float32Array(state.length * 3);

    const geometry = new THREE.BufferGeometry();
    geometry.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    const points = new THREE.Points(geometry, shared(new THREE.PointsMaterial({
        color: 0xa8dcff, size: 5.5, sizeAttenuation: true, map: glowTexture(),
        transparent: true, opacity: 0.95, depthWrite: false, blending: THREE.AdditiveBlending,
    })));
    points.name = 'flowPulses';
    points.frustumCulled = false;
    viz.world.add(points);
    viz.flow = { points, state, positions };
}

function updateFlow(viz, delta) {
    if (!viz.flow) return;
    const { state, positions, points } = viz.flow;
    const vec = new THREE.Vector3();
    for (let i = 0; i < state.length; i += 1) {
        const pulse = state[i];
        pulse.t += pulse.speed * delta;
        if (pulse.t > 1) pulse.t -= 1;
        pulse.curve.getPoint(pulse.t, vec);
        positions[i * 3] = vec.x;
        positions[i * 3 + 1] = vec.y;
        positions[i * 3 + 2] = vec.z;
    }
    points.geometry.attributes.position.needsUpdate = true;
}

/* ------------------------------------------------------------------ *
 * Panel: live libp2p topology
 * ------------------------------------------------------------------ */

const p2pNodes = new Map();

function peerState(node) {
    if (node.type === 'self') return 'self';
    if (node.type === 'disconnected' || node.type === 'stale') return 'offline';
    if (typeof node.reputation === 'number' && node.reputation < -0.25) return 'degraded';
    if (node.type === 'degraded') return 'degraded';
    return 'healthy';
}

function peerTooltip(node) {
    const state = peerState(node);
    if (state === 'self') {
        return [
            '<strong>Local node</strong>',
            `peer id: ${escapeHtml(String(node.id).slice(0, 20))}…`,
            stats.p2p ? `peers: ${stats.p2p.connected} connected / ${stats.p2p.peers} known` : '',
        ].filter(Boolean).join('<br>');
    }
    const rows = [`<strong>${escapeHtml(node.label || node.id)}</strong>`];
    rows.push(`state: ${state}`);
    if (node.region) rows.push(`region: ${escapeHtml(String(node.region))}`);
    if (typeof node.latency_ms === 'number') rows.push(`latency: ${Math.round(node.latency_ms)} ms`);
    if (typeof node.reputation === 'number') rows.push(`reputation: ${node.reputation.toFixed(2)}`);
    if (typeof node.messages_in === 'number' || typeof node.messages_out === 'number') {
        rows.push(`msgs in/out: ${node.messages_in ?? 0} / ${node.messages_out ?? 0}`);
    }
    return rows.join('<br>');
}

function escapeHtml(value) {
    return String(value).replace(/[&<>"']/g, (c) => (
        { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
    ));
}

function updateP2P(viz, data) {
    const nodes = Array.isArray(data.nodes) ? data.nodes : [];
    const edges = Array.isArray(data.edges) ? data.edges : [];
    const selfNode = nodes.find((n) => n.type === 'self');
    const seen = new Set();
    const positions = new Map();

    if (selfNode) {
        seen.add(selfNode.id);
        positions.set(selfNode.id, new THREE.Vector3(0, 0, 0));
        if (!p2pNodes.has(selfNode.id)) {
            const group = makeCoreNode(1);
            group.userData.node = selfNode;
            group.userData.born = performance.now();
            viz.nodeLayer.add(group);
            p2pNodes.set(selfNode.id, group);
        } else {
            p2pNodes.get(selfNode.id).userData.node = selfNode;
        }
    }

    nodes.filter((n) => n.type !== 'self').forEach((node) => {
        seen.add(node.id);
        const state = peerState(node);
        // Offline peers pull inward and sink slightly; healthy peers sit on the
        // main shell. Angular slot is hashed from the id so nodes never shuffle.
        const radius = state === 'offline' ? 78 : 126;
        const target = hashPoint(node.id, radius);
        if (state === 'offline') target.y -= 12;
        positions.set(node.id, target);

        const existing = p2pNodes.get(node.id);
        if (existing && existing.userData.state === state) {
            existing.userData.node = node;
            existing.userData.target = target;
            return;
        }
        if (existing) {
            viz.nodeLayer.remove(existing);
            disposeTree(existing);
        }
        const group = makePeerNode(state);
        group.position.copy(target);
        Object.assign(group.userData, { node, state, target, born: performance.now() });
        viz.nodeLayer.add(group);
        p2pNodes.set(node.id, group);
    });

    p2pNodes.forEach((group, id) => {
        if (seen.has(id)) return;
        viz.nodeLayer.remove(group);
        disposeTree(group);
        p2pNodes.delete(id);
    });

    const byId = new Map(nodes.map((node) => [node.id, node]));
    const links = [];
    edges.forEach((edge) => {
        const from = positions.get(edge.from);
        const to = positions.get(edge.to);
        if (!from || !to) return;
        const up = edge.status === 'connected';
        // Metrics live on the peer end of the edge; the self node carries none.
        const peer = byId.get(edge.to)?.type === 'self' ? byId.get(edge.from) : byId.get(edge.to);
        links.push({
            from,
            to,
            color: up ? PALETTE.peer : PALETTE.offline,
            opacity: up ? 0.42 : 0.16,
            radius: up ? 0.85 : 0.45,
            active: up,
            latencyMs: peer?.latency_ms,
            traffic: (peer?.messages_in ?? 0) + (peer?.messages_out ?? 0),
        });
    });
    const linkStats = buildLinks(viz, links);

    viz.hoverTargets = [];
    p2pNodes.forEach((group) => {
        group.traverse((child) => {
            if (child.isMesh) {
                child.userData.owner = group;
                viz.hoverTargets.push(child);
            }
        });
    });

    return {
        nodes: nodes.length,
        peers: Math.max(nodes.length - (selfNode ? 1 : 0), 0),
        connected: edges.filter((e) => e.status === 'connected').length,
        links: links.length,
        linkBundles: linkStats.bundles,
    };
}

/* ------------------------------------------------------------------ *
 * Panel: Phase-3 reference mesh
 * ------------------------------------------------------------------ */

const meshNodes = new Map();

function updateMesh(viz, data) {
    const cells = Array.isArray(data.cells) ? data.cells : [];
    const rawLinks = Array.isArray(data.links) ? data.links : [];
    const positions = new Map();
    const seen = new Set();

    cells.forEach((cell) => {
        const pos = new THREE.Vector3(Number(cell.x) || 0, Number(cell.y) || 0, Number(cell.z) || 0);
        positions.set(cell.id, pos);
        seen.add(cell.id);
        if (meshNodes.has(cell.id)) {
            meshNodes.get(cell.id).position.copy(pos);
            return;
        }
        const group = cell.role === 'vertex'
            ? makeCoreNode(0.85)
            : makeParentCell(PALETTE.parent);
        group.position.copy(pos);
        Object.assign(group.userData, { cell, born: performance.now() });
        viz.nodeLayer.add(group);
        meshNodes.set(cell.id, group);
    });

    meshNodes.forEach((group, id) => {
        if (seen.has(id)) return;
        viz.nodeLayer.remove(group);
        disposeTree(group);
        meshNodes.delete(id);
    });

    const links = [];
    rawLinks.forEach((link) => {
        const from = positions.get(link.from);
        const to = positions.get(link.to);
        if (!from || !to) return;
        const dependency = link.kind === 'dependency';
        links.push({
            from,
            to,
            color: dependency ? PALETTE.dependency : PALETTE.adjacent,
            opacity: dependency ? 0.5 : 0.22,
            radius: dependency ? 0.95 : 0.5,
            active: dependency,
        });
    });
    const linkStats = buildLinks(viz, links);

    buildTetraHull(viz, cells, positions);

    viz.hoverTargets = [];
    meshNodes.forEach((group) => {
        group.traverse((child) => {
            if (child.isMesh) {
                child.userData.owner = group;
                viz.hoverTargets.push(child);
            }
        });
    });

    return { cells: cells.length, links: links.length, linkBundles: linkStats.bundles };
}

/**
 * Translucent shell across the parent cells. Communicates that the reference
 * layout is a closed polyhedron rather than a loose cloud of markers.
 */
function buildTetraHull(viz, cells, positions) {
    const existing = viz.world.getObjectByName('tetraHull');
    if (existing) {
        viz.world.remove(existing);
        disposeTree(existing);
    }
    const parents = cells.filter((c) => c.role === 'parent').slice(0, 4).map((c) => positions.get(c.id));
    if (parents.length < 4 || parents.some((p) => !p)) return;

    const faces = [[0, 1, 2], [0, 1, 3], [0, 2, 3], [1, 2, 3]];
    const verts = [];
    faces.forEach(([a, b, c]) => {
        verts.push(parents[a].x, parents[a].y, parents[a].z);
        verts.push(parents[b].x, parents[b].y, parents[b].z);
        verts.push(parents[c].x, parents[c].y, parents[c].z);
    });
    const geometry = new THREE.BufferGeometry();
    geometry.setAttribute('position', new THREE.Float32BufferAttribute(verts, 3));
    geometry.computeVertexNormals();
    const hull = new THREE.Mesh(geometry, MAT.hull);
    hull.name = 'tetraHull';

    const edges = new THREE.LineSegments(
        new THREE.EdgesGeometry(geometry, 1),
        lineMaterial(PALETTE.hull, 0.3),
    );
    edges.name = 'tetraHullEdges';
    hull.add(edges);

    viz.world.add(hull);
}

/* ------------------------------------------------------------------ *
 * Interaction
 * ------------------------------------------------------------------ */

const raycaster = new THREE.Raycaster();
const pointer = new THREE.Vector2();

function attachHover(viz, describe) {
    const el = viz.renderer.domElement;
    let active = null;

    function clear() {
        if (active) {
            active.scale.setScalar(active.userData.baseScale ?? 1);
            active = null;
        }
        viz.tooltip.classList.remove('is-open');
    }

    el.addEventListener('pointerleave', clear);
    el.addEventListener('pointermove', (event) => {
        const rect = el.getBoundingClientRect();
        pointer.x = ((event.clientX - rect.left) / rect.width) * 2 - 1;
        pointer.y = -((event.clientY - rect.top) / rect.height) * 2 + 1;
        raycaster.setFromCamera(pointer, viz.camera);
        const hit = raycaster.intersectObjects(viz.hoverTargets, false)[0];
        if (!hit) {
            clear();
            return;
        }
        const owner = hit.object.userData.owner;
        if (!owner) {
            clear();
            return;
        }
        if (owner !== active) {
            clear();
            active = owner;
            active.userData.baseScale = active.userData.baseScale ?? active.scale.x;
            active.scale.setScalar(active.userData.baseScale * 1.25);
        }
        viz.tooltip.innerHTML = describe(owner.userData);
        viz.tooltip.classList.add('is-open');
        const x = Math.min(Math.max(event.clientX - rect.left + 14, 8), rect.width - 190);
        const y = Math.min(Math.max(event.clientY - rect.top + 14, 8), rect.height - 90);
        viz.tooltip.style.transform = `translate(${x}px, ${y}px)`;
    });
}

/* ------------------------------------------------------------------ *
 * Animation loop and lifecycle
 * ------------------------------------------------------------------ */

let p2pViz = null;
let meshViz = null;
let panelVisible = true;
let lastFrame = performance.now();
const stats = { p2p: null, mesh: null };

function tickPanel(viz, delta, elapsed) {
    if (!viz || viz.contextLost) return;
    viz.controls.update();
    updateFlow(viz, delta);

    viz.nodeLayer.children.forEach((group) => {
        if (group.userData.spin && !REDUCED_MOTION) {
            group.userData.spin.rotation.y += delta * 0.35;
            group.userData.spin.rotation.x += delta * 0.11;
        }
        if (group.userData.target) {
            group.position.lerp(group.userData.target, Math.min(1, delta * 3));
        }
        if (group.userData.born) {
            const rest = group.userData.restScale ?? 1;
            const age = (performance.now() - group.userData.born) / 420;
            const eased = age < 1 ? rest * (1 - (1 - age) * (1 - age)) : rest;
            group.scale.setScalar(eased);
            group.userData.baseScale = eased;
            if (age >= 1) group.userData.born = null;
        }
        const ring = group.getObjectByName('stateRing');
        if (ring && !REDUCED_MOTION) {
            ring.scale.setScalar(1 + Math.sin(elapsed * 2.4 + group.position.x * 0.05) * 0.07);
            ring.rotation.z += delta * 0.5;
        }
    });

    viz.renderer.render(viz.scene, viz.camera);
}

function animate() {
    requestAnimationFrame(animate);
    const now = performance.now();
    const delta = Math.min((now - lastFrame) / 1000, 0.05);
    lastFrame = now;
    if (!panelVisible || document.hidden) return;
    const elapsed = now / 1000;
    tickPanel(p2pViz, delta, elapsed);
    tickPanel(meshViz, delta, elapsed);
    publishDiagnostics();
}

/* ------------------------------------------------------------------ *
 * Data polling
 * ------------------------------------------------------------------ */

function showError(id, message) {
    const el = document.getElementById(id);
    if (!el) return;
    if (!message) {
        el.style.display = 'none';
        el.textContent = '';
        return;
    }
    el.style.display = 'block';
    el.textContent = message;
}

function setSummary(id, text) {
    const el = document.getElementById(id);
    if (el) el.textContent = text;
}

/** Render a /api/topology payload, whichever way it arrived. */
function applyP2PData(data) {
    if (!p2pViz) return;
    if (data.error) throw new Error(data.error);
    stats.p2p = updateP2P(p2pViz, data);
    const empty = stats.p2p.peers === 0;
    p2pViz.hint.textContent = empty
        ? 'No peers connected yet — the local node is shown alone.'
        : 'Drag to orbit · scroll to zoom · hover a node for detail';
    setSummary('p2p-3d-summary', empty
        ? 'No peers connected'
        : `${stats.p2p.peers} peers · ${stats.p2p.connected} connected links`);
    showError('p2p-3d-error', '');
}

async function refreshP2P() {
    if (!p2pViz) return;
    try {
        const res = await fetch('/api/topology', fetchOpts);
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || res.statusText);
        applyP2PData(data);
    } catch (e) {
        console.warn('3D topology:', e);
        showError('p2p-3d-error', `3D P2P: ${e.message}`);
    }
}

async function refreshMesh() {
    if (!meshViz) return;
    try {
        const res = await fetch('/api/mesh3d-viz', fetchOpts);
        const data = await res.json();
        if (!res.ok) throw new Error(data.message || res.statusText);
        stats.mesh = updateMesh(meshViz, data);
        const caption = document.getElementById('mesh3d-3d-caption');
        if (caption && (data.description || data.title)) {
            caption.textContent = data.description || data.title;
        }
        setSummary('mesh3d-3d-summary', `${stats.mesh.cells} cells · ${stats.mesh.links} links`);
        showError('mesh3d-3d-error', '');
    } catch (e) {
        console.warn('mesh3d viz:', e);
        showError('mesh3d-3d-error', `3D mesh: ${e.message}`);
    }
}

/* ------------------------------------------------------------------ *
 * Diagnostics
 * ------------------------------------------------------------------ */

let lastDiagnostics = 0;

function publishDiagnostics() {
    const now = performance.now();
    if (now - lastDiagnostics < 1000) return;
    lastDiagnostics = now;

    const snapshot = (viz, panelStats) => {
        if (!viz) return null;
        const info = viz.renderer.info;
        return {
            drawCalls: info.render.calls,
            triangles: info.render.triangles,
            geometries: info.memory.geometries,
            textures: info.memory.textures,
            programs: info.programs ? info.programs.length : null,
            drawingBuffer: `${viz.renderer.domElement.width}x${viz.renderer.domElement.height}`,
            pixelRatio: viz.renderer.getPixelRatio(),
            flowPulses: viz.flow ? viz.flow.state.length : 0,
            data: panelStats,
        };
    };

    window.__QSDM_VIZ_DIAGNOSTICS__ = {
        reducedMotion: REDUCED_MOTION,
        rendering: panelVisible && !document.hidden,
        postProcessing: 'none (emissive + additive sprites instead of a bloom composer)',
        p2p: snapshot(p2pViz, stats.p2p),
        mesh: snapshot(meshViz, stats.mesh),
    };
}

/* ------------------------------------------------------------------ *
 * Boot
 * ------------------------------------------------------------------ */

function webglSupported() {
    try {
        const canvas = document.createElement('canvas');
        return !!(canvas.getContext('webgl2') || canvas.getContext('webgl'));
    } catch {
        return false;
    }
}

function guardContextLoss(viz, label) {
    viz.renderer.domElement.addEventListener('webglcontextlost', (event) => {
        event.preventDefault();
        viz.contextLost = true;
        showError(label, 'WebGL context lost — reload the page to restore this view.');
    });
    viz.renderer.domElement.addEventListener('webglcontextrestored', () => {
        viz.contextLost = false;
        showError(label, '');
    });
}

function init() {
    const p2pContainer = document.getElementById('p2p-3d-container');
    const meshContainer = document.getElementById('mesh3d-3d-container');
    if (!p2pContainer && !meshContainer) return;

    if (!webglSupported()) {
        showError('p2p-3d-error', 'WebGL is unavailable in this browser — 3D views are disabled.');
        showError('mesh3d-3d-error', 'WebGL is unavailable in this browser — 3D views are disabled.');
        return;
    }

    if (p2pContainer) {
        p2pViz = createViz(p2pContainer, {
            camera: [22, 96, 238],
            target: [0, -8, 0],
            hint: 'Drag to orbit · scroll to zoom · hover a node for detail',
            autoRotateSpeed: 0.3,
        });
        p2pViz.world.add(makeRadarFloor(200, -110));
        attachHover(p2pViz, (userData) => (userData.node ? peerTooltip(userData.node) : ''));
        guardContextLoss(p2pViz, 'p2p-3d-error');
        new ResizeObserver(() => resizeViz(p2pViz)).observe(p2pContainer);
    }

    if (meshContainer) {
        meshViz = createViz(meshContainer, {
            camera: [110, 130, 330],
            hint: 'Reference geometry · drag to orbit · scroll to zoom',
            autoRotateSpeed: 0.45,
        });
        meshViz.world.add(makeRadarFloor(230, -175));
        attachHover(meshViz, (userData) => (userData.cell
            ? `<strong>${escapeHtml(userData.cell.label || userData.cell.id)}</strong><br>role: ${escapeHtml(userData.cell.role || 'cell')}`
            : ''));
        guardContextLoss(meshViz, 'mesh3d-3d-error');
        new ResizeObserver(() => resizeViz(meshViz)).observe(meshContainer);
    }

    // An operator dashboard stays open for hours; only render and poll while
    // the card is actually on screen.
    const card = document.getElementById('viz3d-card');
    if (card && 'IntersectionObserver' in window) {
        new IntersectionObserver((entries) => {
            panelVisible = entries.some((entry) => entry.isIntersecting);
        }, { rootMargin: '120px' }).observe(card);
    }

    // dashboard.js owns the /ws connection and pushes topology frames here, so
    // the panel tracks peer churn live instead of waiting out the poll below.
    // Offscreen pushes are dropped rather than queued: rebuilding the scene
    // graph for a panel nobody is looking at is the cost the visibility gate
    // exists to avoid, and the poll repaints within 4s of it coming back.
    window.__QSDM_VIZ_PUSH_TOPOLOGY__ = (data) => {
        if (!p2pViz || !panelVisible || document.hidden) return;
        try {
            applyP2PData(data);
        } catch (e) {
            console.warn('3D topology push:', e);
            showError('p2p-3d-error', `3D P2P: ${e.message}`);
        }
    };

    animate();
    refreshP2P();
    refreshMesh();
    // Fallback and catch-up path: covers a dropped /ws connection, and the
    // mesh panel, which has no push feed.
    setInterval(() => {
        if (!panelVisible || document.hidden) return;
        refreshP2P();
        refreshMesh();
    }, 4000);
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
} else {
    init();
}
