package collect

import (
	"os"
	"path/filepath"
	"testing"

	"lz-monitor/internal/proto"
)

// mkHwmon 搭一个 hwmon 芯片 fixture：name + 任意键值文件。
func mkHwmon(t *testing.T, sysRoot, dir, chip string, files map[string]string) string {
	t.Helper()
	d := filepath.Join(sysRoot, "class", "hwmon", dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "name"), []byte(chip+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for k, v := range files {
		if err := os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func mkFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkDiskFixture 造一块真实 sysfs 拓扑（目录+符号链接）：drivetemp 经 device
// 直配到 sda；NVMe hwmon 挂在控制器上、块设备 device 指向命名空间自身，走前缀匹配。
func mkDiskFixture(t *testing.T, sysRoot string) {
	t.Helper()
	// SATA：hwmonX/device 与 block/sda/device 同指 scsi_device 目录
	scsiDir := filepath.Join(sysRoot, "devices", "pci0", "scsi", "0:0:0:0")
	blk := filepath.Join(scsiDir, "block", "sda")
	mkFile(t, filepath.Join(blk, ".keep"), "")
	if err := os.MkdirAll(filepath.Join(sysRoot, "block"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(blk, filepath.Join(sysRoot, "block", "sda")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(scsiDir, filepath.Join(blk, "device")); err != nil {
		t.Fatal(err)
	}
	hw := mkHwmon(t, sysRoot, "hwmon10", "drivetemp", map[string]string{"temp1_input": "33500"})
	if err := os.Symlink(scsiDir, filepath.Join(hw, "device")); err != nil {
		t.Fatal(err)
	}
	// NVMe：hwmon device=控制器目录；block/nvme0n1 是命名空间目录符号链接，
	// 其 device 指向命名空间自身（真实内核行为），需按前缀关系匹配
	ctlDir := filepath.Join(sysRoot, "devices", "pci1", "nvme", "nvme0")
	nsDir := filepath.Join(ctlDir, "nvme0n1")
	mkFile(t, filepath.Join(nsDir, ".keep"), "")
	if err := os.Symlink(nsDir, filepath.Join(sysRoot, "block", "nvme0n1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nsDir, filepath.Join(nsDir, "device")); err != nil {
		t.Fatal(err)
	}
	hw = mkHwmon(t, sysRoot, "hwmon20", "nvme0", map[string]string{"temp1_input": "41000"})
	if err := os.Symlink(ctlDir, filepath.Join(hw, "device")); err != nil {
		t.Fatal(err)
	}
}

func TestThermalCollector_Hwmon(t *testing.T) {
	root := t.TempDir()
	mkHwmon(t, root, "hwmon0", "coretemp", map[string]string{
		"temp1_input": "45000", "temp1_label": "Package id 0",
		"temp2_input": "41000", "temp2_label": "Core 0",
		"temp3_input": "52000", "temp3_label": "Core 1", // 最热一路
	})
	mkHwmon(t, root, "hwmon1", "acpitz", map[string]string{"temp1_input": "27000"})
	mkHwmon(t, root, "hwmon2", "nct6798", map[string]string{
		"temp1_input": "35000", "temp1_label": "SYSTIN",
		"fan1_input": "1200", "fan2_input": "950", "fan3_input": "0",
	})
	mkDiskFixture(t, root)

	c := &ThermalCollector{sysRoot: root}
	r := &proto.Report{}
	if err := c.Collect(r); err != nil {
		t.Fatal(err)
	}
	type tp struct {
		Sensor string
		Temp   float64
	}
	got := make([]tp, 0, len(r.Temps))
	for _, p := range r.Temps {
		got = append(got, tp{p.Sensor, p.Temp})
	}
	// CPU 三通道收敛为最热 52；drivetemp→sda 33.5；nvme→nvme0n1 41；主板两路保留
	want := []tp{{"CPU", 52}, {"acpitz", 27}, {"nct6798 SYSTIN", 35}, {"nvme0n1", 41}, {"sda", 33.5}}
	if len(got) != len(want) {
		t.Fatalf("temps = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("temps[%d] = %v, want %v", i, got[i], w)
		}
	}
	if len(r.Fans) != 2 || r.Fans[0].Sensor != "nct6798" || r.Fans[0].RPM != 1200 ||
		r.Fans[1].Sensor != "nct6798/fan2" || r.Fans[1].RPM != 950 {
		t.Fatalf("fans = %+v", r.Fans)
	}
}

func TestThermalCollector_NoSensors(t *testing.T) {
	root := t.TempDir()
	c := &ThermalCollector{sysRoot: root}
	r := &proto.Report{}
	if err := c.Collect(r); err != nil {
		t.Fatal(err)
	}
	if r.Temps != nil || r.Fans != nil {
		t.Fatalf("无传感器应留空，got temps=%v fans=%v", r.Temps, r.Fans)
	}
}
