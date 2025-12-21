from __future__ import annotations

import math
from typing import Dict, List, Optional, Tuple

import networkx as nx
from fastapi import FastAPI
from pydantic import BaseModel, Field

app = FastAPI(title="uav-sim-router")

CONTROL_NODE = "__CONTROL__"

class Vec3(BaseModel):
    x: float
    y: float
    z: float

class Drone(BaseModel):
    id: str
    pos: Vec3


class RouteRequest(BaseModel):
    drones: List[Drone]

    control_pos: Vec3 = Field(default_factory=lambda: Vec3(x=0, y=0, z=0))
    control_range: float
    drone_range: float

    src: str
    dst: str

    weighted: bool = False

class RouteMetrics(BaseModel):
    hops: int
    total_dist: float
    bottleneck_dist: float
    bottleneck_margin: float


class RouteResponse(BaseModel):
    ok: bool
    path: List[str] = []
    metrics: Optional[RouteMetrics] = None
    reason: Optional[str] = None

def dist(a: Vec3, b: Vec3) -> float:
    dx = a.x - b.x
    dy = a.y - b.y
    dz = a.z - b.z
    return math.sqrt(dx * dx + dy * dy + dz * dz)

def build_graph(req: RouteRequest) -> Tuple[nx.Graph, Dict[str, Vec3]]:
    g = nx.Graph()

    pos: Dict[str, Vec3] = {CONTROL_NODE: req.control_pos}
    g.add_node(CONTROL_NODE)

    for d in req.drones:
        pos[d.id] = d.pos
        g.add_node(d.id)

    for d in req.drones:
        d_dist = dist(req.control_pos, d.pos)
        if d_dist <= req.control_range:
            g.add_edge(CONTROL_NODE, d.id, weight=d_dist)

    n = len(req.drones)
    for i in range(n):
        a = req.drones[i]
        for j in range(i + 1, n):
            b = req.drones[j]
            d_dist = dist(a.pos, b.pos)
            if d_dist <= req.drone_range:
                g.add_edge(a.id, b.id, weight=d_dist)

    return g, pos

def path_metrics(path: List[str], g: nx.Graph, req: RouteRequest) -> RouteMetrics:
    total = 0.0
    bottleneck = 0.0

    for u, v in zip(path, path[1:]):
        w = float(g[u][v].get("weight", 0,0))
        total += w
        bottleneck = max(bottleneck, w)

    bottleneck_margin = float('inf')
    for u, v in zip(path, path[1:]):
        w = float(g[u][v].get("weight", 0,0))
        cap = req.drone_range
        if u == CONTROL_NODE or v == CONTROL_NODE:
            cap = req.control_range
        bottleneck_margin = min(bottleneck_margin, cap - w)

    return RouteMetrics(
        hops=max(0, len(path) -1),
        total_dist=total,
        bottleneck_dist=bottleneck,
        bottleneck_margin=bottleneck_margin,
    )

@app.post("/route", response_model=RouteResponse)
def route(req: RouteRequest) -> RouteResponse:
    g, _pos = build_graph(req)

    if req.src not in g:
        return RouteResponse(ok=False, reason=f"src not found: {req.src}")
    if req.dst not in g:
        return RouteResponse(ok=False, reason=f"src not found: {req.dst}")

    try:
        if req.weighted:
            path = nx.shortest_path(g, req.src, req.dst, weight="weight")
        else:
            path = nx.shortest_path(g, req.src, req.dst)
    except nx.NetworkXNoPath:
        return RouteResponse(ok=False, reason="no path")
    
    m = path_metrics(path, g, req)
    return RouteResponse(ok=True, path=path, metrics=m)
