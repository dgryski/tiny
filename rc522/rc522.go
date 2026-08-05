package rc522

import (
	"errors"
	"time"
)

var (
	ErrNoTag       = errors.New("no tag in range")
	ErrAuthFailed  = errors.New("authentication failed")
	ErrWrite       = errors.New("write failed")
	ErrReadTimeout = errors.New("read failed: timeout")
)

func (d *Device) Init() error {
	// set intput/output modes
	d.initPins()

	// init triggers hard reset; wait for it to complete
	time.Sleep(50 * time.Millisecond)

	d.writeRegister(regCommand, pcdSoftReset)
	for i := range initSequence {
		d.writeRegister(initSequence[i].reg, initSequence[i].val)
	}

	// set antenna gain?
	d.AntennaOn()

	return nil
}

// TODO(dgryski): read many
func (d *Device) readRegister(reg byte) (byte, error) {
	address := ((reg << 1) & 0x7E) | 0x80
	d.cs.Low()
	d.bus.Transfer(address)
	val, err := d.bus.Transfer(0)
	d.cs.High()
	return val, err
}

func (d *Device) writeRegister(reg, val byte) error {
	address := (reg << 1) & 0x7E
	d.cs.Low()
	d.bus.Transfer(address)
	_, err := d.bus.Transfer(val)
	d.cs.High()
	return err
}

func (d *Device) writeRegisterData(reg byte, vals []byte) error {
	address := (reg << 1) & 0x7E
	d.cs.Low()
	d.bus.Transfer(address)
	for _, val := range vals {
		_, err := d.bus.Transfer(val)
		if err != nil {
			d.cs.High()
			return err
		}
	}
	d.cs.High()
	return nil
}

// TODO(dgryski): add readregister n -> []byte

func (d *Device) GetVersion() (byte, error) {
	return d.readRegister(regVersion)
}

func (d *Device) AntennaOn() error {
	curr, err := d.readRegister(regTxControl)
	if err != nil {
		return err
	}
	if (curr & 0x03) != 0x03 {
		if err := d.writeRegister(regTxControl, curr|0x03); err != nil {
			return err
		}
	}
	return nil
}

func (d *Device) AntennaOff() {
	d.clearFlags(regTxControl, 0x03)
}

func (d *Device) GetAntennaGain() (byte, error) {
	g, err := d.readRegister(regRFCfg)
	return g & (0x07 << 4), err
}

func (d *Device) SetAntennaGain(gain byte) error {
	g, err := d.GetAntennaGain()
	if err != nil {
		return err
	}

	if g != gain {
		// clear needed to allow 000 pattern;
		if err := d.clearFlags(regRFCfg, (0x07 << 4)); err != nil {
			return err
		}
		if err := d.setFlags(regRFCfg, gain&(0x07<<4)); err != nil {
			return err
		}
	}
	return nil
}

func (d *Device) request() (bool, error) {
	if err := d.writeRegister(regTxMode, 0x00); err != nil {
		return false, err
	}
	if err := d.writeRegister(regRxMode, 0x00); err != nil {
		return false, err
	}
	if err := d.writeRegister(regModWidth, 0x26); err != nil {
		return false, err
	}
	if err := d.clearFlags(regColl, 0x80); err != nil {
		return false, err
	}

	_, backBits, err := d.communicateWithPICC(pcdTransceive, []byte{piccREQA}, 0, 0)
	if err != nil {
		return false, err
	}
	if backBits != 0x10 {
		return false, errors.New("wrong number of bits read")
	}
	return true, nil
}

func (d *Device) communicateWithPICC(cmd byte, data []byte, validBits byte, rxAlign byte) ([]byte, int, error) {

	var irqEn byte
	var irqWait byte

	switch cmd {
	case pcdMFAuthent:
		irqEn = 0x12
		irqWait = 0x10
	case pcdTransceive:
		irqEn = 0x77
		irqWait = 0x30
	}

	// TODO(dgryski): set command list slice?

	if err := d.writeRegister(regCommand, pcdIdle); err != nil {
		return nil, -1, err
	}

	if err := d.writeRegister(regComIrq, 0x7F); err != nil {
		return nil, -1, err
	}

	if err := d.writeRegister(regFIFOLevel, 0x80); err != nil {
		return nil, -1, err
	}

	if err := d.writeRegisterData(regFIFOData, data); err != nil {
		return nil, -1, err
	}

	var txLastBits byte
	if data[0] == piccREQA || data[0] == piccWUPA {
		txLastBits = 7
	}

	if err := d.writeRegister(regBitFraming, txLastBits); err != nil {
		return nil, -1, err
	}

	if err := d.writeRegister(regCommand, cmd); err != nil {
		return nil, -1, err
	}

	if cmd == pcdTransceive {
		if err := d.setFlags(regBitFraming, 0x80); err != nil {
			return nil, -1, err
		}
	}

	var n byte

	deadline := time.Now().Add(36 * time.Millisecond)
	var completed bool
	for time.Now().Before(deadline) {
		var err error
		if n, err = d.readRegister(regComIrq); err != nil {
			return nil, -1, err
		}
		if (n & irqWait) != 0 {
			completed = true
			break
		}
		if n&0x01 != 0 {
			return nil, -1, ErrReadTimeout
		}
		time.Sleep(1 * time.Microsecond)
	}

	if !completed {
		return nil, -1, ErrReadTimeout
	}

	if errReg, err := d.readRegister(regError); err != nil || errReg&0x13 != 0 {
		return nil, -1, err
	}

	if n&irqEn&0x01 == 1 {
		return nil, -1, errors.New("irq error")
	}

	var backData []byte
	var backLength int

	if cmd == pcdTransceive {
		n, err := d.readRegister(regFIFOLevel)
		if err != nil {
			return nil, -1, err
		}
		if n == 0 {
			n = 1
		}
		if n > 16 {
			n = 16
		}

		backData = make([]byte, n)
		for i := byte(0); i < n; i++ {
			byteVal, err := d.readRegister(regFIFOData)
			if err != nil {
				return nil, -1, err
			}
			backData[i] = byteVal
		}

		lastBits, err := d.readRegister(regControl)
		if err != nil {
			return nil, -1, err
		}
		lastBits = lastBits & 0x07
		if lastBits != 0 {
			backLength = (int(n)-1)*8 + int(lastBits)
		} else {
			backLength = int(n) * 8
		}

	}

	return backData, backLength, nil
}

func (d *Device) setFlags(register byte, mask byte) error {
	current, err := d.readRegister(register)
	if err != nil {
		return err
	}
	return d.writeRegister(register, current|mask)
}

func (d *Device) clearFlags(register byte, mask byte) error {
	current, err := d.readRegister(register)
	if err != nil {
		return err
	}
	return d.writeRegister(register, current&^mask)
}

func (d *Device) ReadUID(timeout time.Duration) (uid []byte, err error) {
	uuid, err := d.selectCard(timeout)
	return uuid, err
}

// selectCard selects the card after the IRQ event was received.
func (d *Device) selectCard(timeout time.Duration) ([]byte, error) {
	if _, err := d.request(); err != nil {
		return nil, err
	}
	uuid, err := d.anticollision(piccSEL_CL1)

	if err != nil {
		return nil, err
	}
	if _, err := d.selectTag(uuid, piccSEL_CL1); err != nil {
		return nil, err
	}

	if uuid[0] == 0x88 { // Incomplete UID
		// Get remaining bytes
		uuidEnd, err := d.anticollision(piccSEL_CL2)
		if err != nil {
			return nil, err
		}

		// TODO(dgryski): check for uuidEnd[0] == 0x88 needing PICC_CMD_SEL_CL3

		// strip off 0x88 and final crc byte
		uuid = uuid[1 : len(uuid)-1]
		uuid = append(uuid, uuidEnd[:len(uuidEnd)-1]...)

		if _, err := d.selectTag(uuid, piccSEL_CL2); err != nil {
			return nil, err
		}

		return uuid, nil
	}

	return uuid[:len(uuid)-1], nil
}

func (d *Device) anticollision(cascadeLevel byte) ([]byte, error) {
	if err := d.clearFlags(regColl, 0x80); err != nil {
		return nil, err
	}

	if err := d.writeRegister(regBitFraming, 0x00); err != nil {
		return nil, err
	}

	backData, _, err := d.communicateWithPICC(pcdTransceive, []byte{cascadeLevel, 0x20}, 0, 0)
	if err != nil {
		return nil, err
	}

	if len(backData) != 5 {
		return nil, errors.New("bad data length")
	}

	var crc byte
	for _, v := range backData[:4] {
		crc = crc ^ v
	}

	if crc != backData[4] {
		return nil, errors.New("crc mismatch")
	}

	return backData, nil
}

// selectTag selects the FOB device by device UUID.
func (d *Device) selectTag(serial []byte, cl byte) (byte, error) {
	var bptr int
	buf := make([]byte, 7+2)
	buf[0] = cl
	buf[1] = 0x70
	bptr += 2
	copy(buf[bptr:], serial)
	bptr += len(serial)
	lsb, msb, err := d.calculateCRC(buf[:bptr])
	if err != nil {
		return 0, err
	}
	buf[bptr] = lsb
	bptr++
	buf[bptr] = msb
	bptr++

	// TODO(dgryski): this should be
	// PCD_WriteRegister(BitFramingReg, (rxAlign << 4) + txLastBits);
	// RxAlign = BitFramingReg[6..4]. TxLastBits = BitFramingReg[2..0]

	if err := d.writeRegister(regBitFraming, 0x00); err != nil {
		return 0, err
	}

	backData, backLen, err := d.communicateWithPICC(pcdTransceive, buf[:bptr], 0, 0)
	if err != nil {
		return 0, err
	}

	var blocks byte
	if backLen == 0x18 {
		blocks = backData[0]
	}
	return blocks, nil
}

// calculateCRC calculates the CRC of the data using the card chip.
func (d *Device) calculateCRC(data []byte) (byte, byte, error) {

	// TODO(dgryski): slice of commands

	if err := d.writeRegister(regCommand, pcdIdle); err != nil {
		return 0, 0, err
	}

	if err := d.writeRegister(regDivIrq, 0x04); err != nil {
		return 0, 0, err
	}
	if err := d.writeRegister(regFIFOLevel, 0x80); err != nil {
		return 0, 0, err
	}

	if err := d.writeRegisterData(regFIFOData, data); err != nil {
		return 0, 0, err
	}

	if err := d.writeRegister(regCommand, pcdCalcCRC); err != nil {
		return 0, 0, err
	}

	deadline := time.Now().Add(89 * time.Millisecond)

	var completed bool
	for time.Now().Before(deadline) {
		n, err := d.readRegister(regDivIrq)
		if err != nil {
			return 0, 0, err
		}
		if n&0x04 != 0 {
			completed = true
			break
		}
	}

	if !completed {
		return 0, 0, ErrReadTimeout
	}

	if err := d.writeRegister(regCommand, pcdIdle); err != nil {
		return 0, 0, err
	}

	lsb, err := d.readRegister(regCRCResultL)
	if err != nil {
		return 0, 0, err
	}

	msb, err := d.readRegister(regCRCResultH)
	if err != nil {
		return 0, 0, err
	}
	return lsb, msb, nil
}

func (d *Device) Authenticate(mode byte, block byte, key []byte, uid []byte) error {
	buf := make([]byte, 12)
	buf[0] = mode
	buf[1] = block
	copy(buf[2:], key)
	copy(buf[8:], uid[:4])

	if _, _, err := d.communicateWithPICC(pcdMFAuthent, buf, 0, 0); err != nil {
		return ErrAuthFailed
	}
	time.Sleep(25 * time.Millisecond)

	status, err := d.readRegister(regStatus2)
	if err != nil {
		return ErrAuthFailed
	}

	if (status & 0x08) == 0 {
		return ErrAuthFailed
	}
	return nil
}

// TODO: sector/alignment stuff
func (d *Device) ReadBlock(block byte) ([]byte, error) {
	buf := make([]byte, 4)
	buf[0] = piccMF_READ
	buf[1] = block
	lsb, msb, err := d.calculateCRC(buf[:2])
	if err != nil {
		return nil, err
	}
	buf[2] = lsb
	buf[3] = msb

	backData, _, err := d.communicateWithPICC(pcdTransceive, buf, 0, 0)
	if err != nil {
		return nil, err
	}

	return backData, nil
}

/*
func (d *Device) WriteBlock(block byte, data [16]byte) error {
	// Step 1: Command
	d.writeRegister(FIFODataReg, PICC_WRITE)
	d.writeRegister(FIFODataReg, block)
	d.writeRegister(CommandReg, Transceive)
	d.writeRegister(BitFramingReg, 0x80)
	time.Sleep(10 * time.Millisecond)

	// Step 2: Data
	for _, b := range data {
		d.writeRegister(FIFODataReg, b)
	}
	d.writeRegister(CommandReg, Transceive)
	d.writeRegister(BitFramingReg, 0x80)
	return nil
}
*/

func (d *Device) HaltA() error {
	buf := make([]byte, 4)
	buf[0] = piccHALT
	buf[1] = 0x00

	lsb, msb, err := d.calculateCRC(buf[:2])
	if err != nil {
		return err
	}
	buf[2] = lsb
	buf[3] = msb

	_, _, err = d.communicateWithPICC(pcdTransceive, buf, 0, 0)
	if err != nil {
		if errors.Is(err, ErrReadTimeout) {
			return nil
		}
		return err
	}

	return nil
}

func (d *Device) StopCrypto() {
	d.clearFlags(regStatus2, 0x08)
}

var initSequence = []struct{ reg, val byte }{
	{regTxMode, 0x00},
	{regRxMode, 0x00},
	{regModWidth, 0x26},
	{regTMode, 0x80},
	{regTPrescaler, 0xA9},
	{regTReloadH, 0x03},
	{regTReloadL, 0xE8},

	{regTxASK, 0x40},
	{regMode, 0x3D},
}
